package gamedetails

import (
	"context"
	"fmt"
	"strings"

	"github.com/nekrozis/goggo/internal/config"
	"github.com/nekrozis/goggo/internal/jsonval"
	"github.com/nekrozis/goggo/internal/util"
)

// This file is the JSON conversion of the GameDetails domain (GD1, the "S-GD2"
// of the original GD ladder): a Galaxy product document becomes a GameDetails
// tree. It reads only the fields the model needs.
//
// The network is not here. Upstream resolves each file entry's downlink inside
// the conversion (galaxyapi.cpp:529-533 calls getResponseJson per file), which
// would drag the API client, its token handling and a future cache into this
// package. Instead the one capability the conversion needs is injected, so this
// package keeps depending on config/util alone and stays testable offline.

// ResolvedFile is what the resolver reports about one file entry.
//
// URL is an intermediate value: it exists so the resolver can derive Path from
// it. The persistent field is Path — GameFile keeps the *original* downlink and
// never the resolved URL (review GD1 §4.2, ruling A1).
type ResolvedFile struct {
	URL  string
	Path string
}

// DownlinkResolver turns one file entry's downlink JSON url into the source the
// file will be fetched from. The implementation belongs to the caller (GD2 owns
// the API call and the cache); the conversion only calls it.
//
// An error skips that one file and nothing else — the upstream `continue` on a
// downlink document that came back empty (galaxyapi.cpp:530-533).
type DownlinkResolver func(ctx context.Context, gamename, downlinkURL string) (ResolvedFile, error)

// logoNameInAPI and logoNameFinal are the upstream logo cleanup: the download
// API advertises a "_glx_logo.jpg" variant whose full-size counterpart is the
// plain ".jpg" name (galaxyapi.cpp:400-401).
const (
	logoNameInAPI = "_glx_logo.jpg"
	logoNameFinal = ".jpg"
)

// httpsPrefix is prepended to the two image paths unconditionally: the API
// answers with a scheme-relative path ("//images..."), so the concatenation is
// what makes it a URL. An absolute value would gain a second prefix — upstream
// does exactly that, and nothing in the API sends one.
const httpsPrefix = "https:"

// ProductInfoToGameDetails converts one Galaxy product document into the domain
// model (galaxyapi.cpp:391-500, productInfoJsonToGameDetails).
//
// Type checking follows one rule (review GD1 §3): a field the conversion reads
// is validated against the JSON type it must have — absent is the zero value
// (upstream reads a missing member leniently), present-with-the-wrong-shape is
// an error. Values are never coerced into something plausible. Fields the
// conversion does not read are not validated at all.
//
// owned is the set of owned product ids. An EMPTY set means no filtering, which
// is what upstream tests before consulting the list (galaxyapi.cpp:445-450).
func ProductInfoToGameDetails(ctx context.Context, product map[string]any, cfg config.DownloadConfig,
	owned map[string]bool, resolve DownlinkResolver) (GameDetails, error) {
	gd, err := convertProduct(ctx, product, cfg, owned, resolve)
	if err != nil {
		// Nothing half-built crosses the boundary: a caller that gets an error
		// gets the zero value, never a partial tree it could mistake for a
		// result (review GD1, transaction boundary).
		return GameDetails{}, err
	}
	return gd, nil
}

// convertProduct is the recursive body: a DLC subtree is converted by this same
// function, exactly as upstream recurses (galaxyapi.cpp:455-460).
func convertProduct(ctx context.Context, product map[string]any, cfg config.DownloadConfig,
	owned map[string]bool, resolve DownlinkResolver) (GameDetails, error) {
	var gd GameDetails

	gamename, err := fieldString(product, "slug")
	if err != nil {
		return GameDetails{}, wrap("gamedetails", err)
	}
	gd.Gamename = gamename

	// The product id is one of the identifier fields and reads through
	// idString (DEFECT-GD3-1: the live API sends it as a JSON number); title
	// and changelog are free strings and keep the strict reader.
	productID, err := idString(product, "id")
	if err != nil {
		return GameDetails{}, wrap("gamedetails", err)
	}
	gd.ProductID = productID
	for _, f := range []struct {
		name string
		dst  *string
	}{
		{"title", &gd.Title},
		{"changelog", &gd.Changelog},
	} {
		value, err := fieldString(product, f.name)
		if err != nil {
			return GameDetails{}, wrap("gamedetails", err)
		}
		*f.dst = value
	}

	images, err := fieldObject(product, "images")
	if err != nil {
		return GameDetails{}, wrap("gamedetails", err)
	}
	icon, err := fieldString(images, "icon")
	if err != nil {
		return GameDetails{}, wrap("gamedetails: images", err)
	}
	gd.Icon = httpsPrefix + icon
	logo, err := fieldString(images, "logo")
	if err != nil {
		return GameDetails{}, wrap("gamedetails: images", err)
	}
	gd.Logo = strings.ReplaceAll(httpsPrefix+logo, logoNameInAPI, logoNameFinal)

	// The save-product-json artifact is rendered from the document the
	// conversion already holds — for a DLC that is the inline expanded
	// document, so GD5 adds no request of its own (ruling 5, the approved
	// divergence from upstream's per-DLC getProductInfo).
	if cfg.SaveProductJSON {
		rendered, err := util.StyledJSON(product)
		if err != nil {
			return GameDetails{}, wrap("gamedetails: product json", err)
		}
		gd.ProductJson = rendered
	}

	downloads, err := fieldObject(product, "downloads")
	if err != nil {
		return GameDetails{}, wrap("gamedetails", err)
	}

	// Each vector is gated by its COMPOSITE mask and typed with the base bit,
	// the way upstream does it (galaxyapi.cpp:404-425). The composite gate is
	// deliberate: with the DLC bit set and the base bit clear the base vector is
	// still converted, exactly as the C++ source behaves.
	for _, v := range []struct {
		gate uint32
		spec uint32
		key  string
		dst  *[]GameFile
	}{
		{config.GFInstaller, config.GFBaseInstaller, "installers", &gd.Installers},
		{config.GFExtra, config.GFBaseExtra, "bonus_content", &gd.Extras},
		{config.GFPatch, config.GFBasePatch, "patches", &gd.Patches},
		{config.GFLangPack, config.GFBaseLangPack, "language_packs", &gd.LanguagePacks},
	} {
		if cfg.Include&v.gate == 0 {
			continue
		}
		nodes, err := fieldArray(downloads, v.key)
		if err != nil {
			return GameDetails{}, wrap("gamedetails: downloads", err)
		}
		files, err := gameFiles(ctx, gamename, gd.Title, v.key, nodes, v.spec, cfg, resolve)
		if err != nil {
			return GameDetails{}, err
		}
		*v.dst = files
	}

	if cfg.Include&config.GFDLC == 0 {
		return gd, nil
	}
	dlcs, err := fieldArray(product, "expanded_dlcs")
	if err != nil {
		return GameDetails{}, wrap("gamedetails", err)
	}
	for i, node := range dlcs {
		dlc, err := jsonval.Object(node)
		if err != nil {
			return GameDetails{}, wrap(fmt.Sprintf("gamedetails: expanded_dlcs[%d]", i), err)
		}
		id, err := idString(dlc, "id")
		if err != nil {
			return GameDetails{}, wrap(fmt.Sprintf("gamedetails: expanded_dlcs[%d]", i), err)
		}
		if len(owned) > 0 && !owned[id] {
			continue
		}
		sub, err := convertProduct(ctx, dlc, cfg, owned, resolve)
		if err != nil {
			return GameDetails{}, err
		}
		sub.TitleBasegame = gd.Title
		sub.GamenameBasegame = gd.Gamename
		retypeDLC(&sub)
		// A DLC with no files at all is dropped (galaxyapi.cpp:492-494).
		if len(sub.Installers)+len(sub.Extras)+len(sub.Patches)+len(sub.LanguagePacks) == 0 {
			continue
		}
		gd.DLCs = append(gd.DLCs, sub)
	}
	return gd, nil
}

// gameFiles converts one downloads vector: the platform/language filter, the
// empty-node skip and the per-file resolution (galaxyapi.cpp:502-593).
func gameFiles(ctx context.Context, gamename, title, label string, nodes []any, typeValue uint32,
	cfg config.DownloadConfig, resolve DownlinkResolver) ([]GameFile, error) {
	var out []GameFile
	// Extras carry no platform or language and are exempt from both filters
	// (galaxyapi.cpp:516-537).
	isExtra := typeValue&config.GFBaseExtra != 0

	for i, node := range nodes {
		info, err := jsonval.Object(node)
		if err != nil {
			return nil, wrap(fmt.Sprintf("gamedetails: %s[%d]", label, i), err)
		}
		name, err := fieldString(info, "name")
		if err != nil {
			return nil, wrap(fmt.Sprintf("gamedetails: %s[%d]", label, i), err)
		}
		version, err := fieldString(info, "version")
		if err != nil {
			return nil, wrap(fmt.Sprintf("gamedetails: %s[%d]", label, i), err)
		}

		platform, language := config.PlatformWindows, config.LangEN
		if !isExtra {
			osName, err := fieldString(info, "os")
			if err != nil {
				return nil, wrap(fmt.Sprintf("gamedetails: %s[%d]", label, i), err)
			}
			langName, err := fieldString(info, "language")
			if err != nil {
				return nil, wrap(fmt.Sprintf("gamedetails: %s[%d]", label, i), err)
			}
			platform = util.OptionValue(osName, config.Platforms, false)
			language = util.OptionValue(langName, config.Languages, false)
			if platform&cfg.InstallerPlatform == 0 || language&cfg.InstallerLanguage == 0 {
				continue
			}
		}

		count, err := fieldInt(info, "count")
		if err != nil {
			return nil, wrap(fmt.Sprintf("gamedetails: %s[%d]", label, i), err)
		}
		totalSize, err := fieldInt(info, "total_size")
		if err != nil {
			return nil, wrap(fmt.Sprintf("gamedetails: %s[%d]", label, i), err)
		}
		// An entry that advertises nothing is skipped; upstream added this for
		// github.com/Sude-/lgogdownloader/issues/200.
		if count == 0 && totalSize == 0 {
			continue
		}

		files, err := fieldArray(info, "files")
		if err != nil {
			return nil, wrap(fmt.Sprintf("gamedetails: %s[%d]", label, i), err)
		}
		for j, fileNode := range files {
			entry, err := jsonval.Object(fileNode)
			if err != nil {
				return nil, wrap(fmt.Sprintf("gamedetails: %s[%d].files[%d]", label, i, j), err)
			}
			where := fmt.Sprintf("gamedetails: %s[%d].files[%d]", label, i, j)
			id, err := idString(entry, "id")
			if err != nil {
				return nil, wrap(where, err)
			}
			downlink, err := fieldString(entry, "downlink")
			if err != nil {
				return nil, wrap(where, err)
			}
			resolved, err := resolve(ctx, gamename, downlink)
			if err != nil {
				// Upstream skips the file when the downlink document came back
				// empty; an unusable entry is that same situation here.
				continue
			}
			if unusablePath(resolved.Path) {
				continue
			}

			gf := GameFile{
				Gamename:              gamename,
				Type:                  typeValue,
				ID:                    id,
				Name:                  name,
				Path:                  resolved.Path,
				Size:                  util.JSONUintString(entry["size"]),
				Version:               version,
				Title:                 title,
				GalaxyDownlinkJSONURL: downlink,
			}
			if !isExtra {
				gf.Platform, gf.Language = platform, language
			}
			if cfg.DuplicateHandler {
				if dup := indexByPath(out, gf.Path); dup >= 0 {
					if !isExtra {
						// The duplicate handler widens the installer's language
						// set instead of adding a second row (galaxyapi.cpp:578-586).
						out[dup].Language |= gf.Language
					}
					continue
				}
			}
			out = append(out, gf)
		}
	}
	return out, nil
}

// retypeDLC marks a converted subtree as DLC content (galaxyapi.cpp:465-488).
func retypeDLC(gd *GameDetails) {
	for _, v := range []struct {
		files []GameFile
		spec  uint32
	}{
		{gd.Installers, config.GFDLCInstaller},
		{gd.Extras, config.GFDLCExtra},
		{gd.Patches, config.GFDLCPatch},
		{gd.LanguagePacks, config.GFDLCLangPack},
	} {
		for i := range v.files {
			v.files[i].Type = v.spec
			v.files[i].TitleBasegame = gd.TitleBasegame
			v.files[i].GamenameBasegame = gd.GamenameBasegame
		}
	}
}

// unusablePath reports the paths upstream rejects with the "/securex?$" pattern
// (galaxyapi.cpp:552-557): a path ending in "/secure" or "/securex" means the
// downlink resolved to something that is not a file.
func unusablePath(path string) bool {
	lower := strings.ToLower(path)
	return strings.HasSuffix(lower, "/secure") || strings.HasSuffix(lower, "/securex")
}

// indexByPath returns the index of the entry with this path, or -1.
func indexByPath(files []GameFile, path string) int {
	for i := range files {
		if files[i].Path == path {
			return i
		}
	}
	return -1
}

// wrap prefixes an error with where the conversion failed.
func wrap(where string, err error) error {
	return fmt.Errorf("%s: %w", where, err)
}

// The four readers below share one rule (review GD1 §3): a missing member (or a
// JSON null) is the zero value; a present member of the wrong JSON type is an
// error.
//
// The shape gate is a Go-side type assertion, not jsonval's conversion: jsonval
// deliberately mirrors jsoncpp's coercing accessors (a number is readable as a
// string, "true" comes out of a bool), and that leniency is exactly what must
// not decide what a field is. Shape first, then jsonval for the value.

// fieldString reads a string field.
func fieldString(obj map[string]any, name string) (string, error) {
	raw, ok := obj[name]
	if !ok || raw == nil {
		return "", nil
	}
	if _, isString := raw.(string); !isString {
		return "", fmt.Errorf("%s: expected a JSON string, got %s", name, jsonval.Kind(raw))
	}
	return jsonval.Str(raw)
}

// idString reads one of the API's identifier fields — the product id, a DLC
// id, a file id — which the C++ conversion reads with jsoncpp's asString()
// (galaxyapi.cpp:416, 460 and 563). That is a conversion, not a type test:
// the live product documents send these ids as JSON numbers (DEFECT-GD3-1,
// evidence dev/audit/evidence/GD3-probe-terraria.err), and the same file
// vector even mixes the shapes — an installer's id is the string
// "en1installer0" while a bonus-content file's id is the number 13403 —
// so the identifier family is defined by what upstream does with it:
//
//	string  → as-is
//	number  → stringified the way jsonval.Str stringifies it
//	bool    → "true" / "false" (the port's locked precedent: id:true → "true")
//	missing → ""
//	null    → "" (asString on a null)
//	object / array → error (jsoncpp would crash; this port reports)
//
// The split is deliberate and narrow (review GD3-R1 §4): slug, title,
// changelog, os, language, name, version and downlink stay free strings under
// fieldString's strict gate. This reader does not reopen GD1's field-type
// rule; it locates `id` as the one family upstream never gated.
func idString(obj map[string]any, name string) (string, error) {
	raw, ok := obj[name]
	if !ok {
		return "", nil
	}
	value, err := jsonval.Str(raw)
	if err != nil {
		return "", fmt.Errorf("%s: %w", name, err)
	}
	return value, nil
}

// fieldObject reads an object field. jsonval.Object is itself a shape
// assertion, so it needs no separate gate. A missing member yields a nil map,
// which every reader below treats as empty.
func fieldObject(obj map[string]any, name string) (map[string]any, error) {
	raw, ok := obj[name]
	if !ok || raw == nil {
		return nil, nil
	}
	value, err := jsonval.Object(raw)
	if err != nil {
		return nil, wrap(name, err)
	}
	return value, nil
}

// fieldArray reads an array field; jsonval.Array is the shape assertion.
func fieldArray(obj map[string]any, name string) ([]any, error) {
	raw, ok := obj[name]
	if !ok || raw == nil {
		return nil, nil
	}
	value, err := jsonval.Array(raw)
	if err != nil {
		return nil, wrap(name, err)
	}
	return value, nil
}

// fieldInt reads an integer field under the same rule as fieldString: a number,
// never a numeric string.
func fieldInt(obj map[string]any, name string) (int64, error) {
	raw, ok := obj[name]
	if !ok || raw == nil {
		return 0, nil
	}
	if !jsonval.IsNumber(raw) {
		return 0, fmt.Errorf("%s: expected a JSON number, got %s", name, jsonval.Kind(raw))
	}
	value, err := jsonval.Int(raw)
	if err != nil {
		return 0, wrap(name, err)
	}
	return value, nil
}
