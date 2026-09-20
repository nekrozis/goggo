package gamedetails

import (
	"context"
	"encoding/json/jsontext"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/nekrozis/goggo/internal/config"
	"github.com/nekrozis/goggo/internal/util"
)

// This file is the JSON conversion of the GameDetails domain: a Galaxy product
// document becomes a GameDetails tree. It reads only the fields the model needs, and
// the one capability it lacks — resolving a file entry's downlink — is injected as a
// DownlinkResolver, so no network code lives here.

// ResolvedFile is what the resolver reports about one file entry.
//
// URL is an intermediate value: it exists so the resolver can derive Path from
// it. The persistent field is Path — GameFile keeps the *original* downlink and
// never the resolved URL.
type ResolvedFile struct {
	URL  string
	Path string
}

// DownlinkResolver turns one file entry's downlink JSON url into the source the
// file will be fetched from. The caller owns the API call and the cache; the
// conversion only calls it.
//
// An error skips that one file and nothing else, the same outcome as a downlink
// document that came back empty.
type DownlinkResolver func(ctx context.Context, gamename, downlinkURL string) (ResolvedFile, error)

// logoNameInAPI and logoNameFinal are the logo cleanup: the download API
// advertises a "_glx_logo.jpg" variant whose full-size counterpart is the plain
// ".jpg" name.
const (
	logoNameInAPI = "_glx_logo.jpg"
	logoNameFinal = ".jpg"
)

// httpsPrefix is prepended to the two image paths unconditionally: the API
// answers with a scheme-relative path ("//images..."), so the concatenation is
// what makes it a URL. An absolute value would gain a second prefix.
const httpsPrefix = "https:"

// ProductInfoToGameDetails converts one Galaxy product document into the domain
// model: a field the conversion reads is validated against the JSON type it must
// have — absent is the zero value, present with the wrong shape is an error — and
// values are never coerced.
//
// owned is the set of owned product ids; an EMPTY set means no filtering.
func ProductInfoToGameDetails(ctx context.Context, product map[string]jsontext.Value, cfg config.DownloadConfig,
	owned map[string]bool, resolve DownlinkResolver) (GameDetails, error) {
	gd, err := convertProduct(ctx, product, cfg, owned, resolve)
	if err != nil {
		// Nothing half-built crosses the boundary: a caller that gets an error
		// gets the zero value, never a partial tree it could mistake for a
		// result.
		return GameDetails{}, err
	}
	return gd, nil
}

// convertProduct is the recursive body: a DLC subtree is converted by this same
// function.
func convertProduct(ctx context.Context, product map[string]jsontext.Value, cfg config.DownloadConfig,
	owned map[string]bool, resolve DownlinkResolver) (GameDetails, error) {
	var gd GameDetails

	gamename, err := fieldString(product, "slug")
	if err != nil {
		return GameDetails{}, wrap("gamedetails", err)
	}
	gd.Gamename = gamename

	// The product id is one of the identifier fields and reads through
	// idString; title and changelog are free strings and keep the strict
	// reader.
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
	// document — so no extra request is made for a DLC.
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

	// Each vector is gated by its COMPOSITE mask and typed with the base bit.
	// The composite gate is deliberate: with the DLC bit set and the base bit
	// clear the base vector is still converted.
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
		dlc, err := memberObject(node)
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
		// A DLC with no files at all is dropped.
		if len(sub.Installers)+len(sub.Extras)+len(sub.Patches)+len(sub.LanguagePacks) == 0 {
			continue
		}
		gd.DLCs = append(gd.DLCs, sub)
	}
	return gd, nil
}

// gameFiles converts one downloads vector: the platform/language filter, the
// empty-node skip and the per-file resolution.
func gameFiles(ctx context.Context, gamename, title, label string, nodes []jsontext.Value, typeValue uint32,
	cfg config.DownloadConfig, resolve DownlinkResolver) ([]GameFile, error) {
	var out []GameFile
	// Extras carry no platform or language and are exempt from both filters.
	isExtra := typeValue&config.GFBaseExtra != 0

	for i, node := range nodes {
		info, err := memberObject(node)
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
		// An entry that advertises nothing is skipped.
		if count == 0 && totalSize == 0 {
			continue
		}

		files, err := fieldArray(info, "files")
		if err != nil {
			return nil, wrap(fmt.Sprintf("gamedetails: %s[%d]", label, i), err)
		}
		for j, fileNode := range files {
			entry, err := memberObject(fileNode)
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
				// An unusable downlink skips the file, the same outcome as an
				// empty downlink document.
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
				Size:                  sizeString(entry["size"]),
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
						// set instead of adding a second row.
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

// retypeDLC marks a converted subtree as DLC content.
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

// unusablePath reports whether a resolved path points at something that is not
// a file: a path ending in "/secure" or "/securex".
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

// The four readers below share one rule: a missing member (or a JSON null) is the
// zero value; a present member of the wrong JSON type is an error.
//
// The shape gate is this package's own reader, not a coercion: a number is NOT
// readable as a string here, and that strictness must not be relaxed by accident.

// fieldString reads a string field.
func fieldString(obj map[string]jsontext.Value, name string) (string, error) {
	value, err := stringOnly(obj[name])
	if err != nil {
		return "", wrap(name, err)
	}
	return value, nil
}

// idString reads one of the API's identifier fields — the product id, a DLC id, a
// file id. It is a conversion, not a type test: the live product documents send these
// ids as JSON numbers, and the same vector even mixes the shapes — an installer's id
// is the string "en1installer0" while a bonus-content file's id is the number 13403.
//
// Accepted shapes: a string as itself and a number as the literal the document
// carried; missing and null are "". A boolean, an object and an array are errors.
// The number keeps its own text so an identifier wider than 2^53 does not lose
// digits. The split is deliberate and narrow: slug, title, changelog, os, language,
// name, version and downlink stay free strings under fieldString's strict gate, and
// `id` is the one family read through a conversion.
func idString(obj map[string]jsontext.Value, name string) (string, error) {
	value, err := identifierText(obj[name])
	if err != nil {
		return "", fmt.Errorf("%s: %w", name, err)
	}
	return value, nil
}

// fieldObject reads an object field. memberObject is itself a shape assertion, so
// it needs no separate gate. A missing member yields a nil map, which every reader
// below treats as empty.
func fieldObject(obj map[string]jsontext.Value, name string) (map[string]jsontext.Value, error) {
	value, err := memberObject(obj[name])
	if err != nil {
		return nil, wrap(name, err)
	}
	return value, nil
}

// fieldArray reads an array field; memberArray is the shape assertion.
func fieldArray(obj map[string]jsontext.Value, name string) ([]jsontext.Value, error) {
	value, err := memberArray(obj[name])
	if err != nil {
		return nil, wrap(name, err)
	}
	return value, nil
}

// fieldInt reads an integer field under the same rule as fieldString: a number,
// never a numeric string.
func fieldInt(obj map[string]jsontext.Value, name string) (int64, error) {
	raw := obj[name]
	if raw.Kind() == jsontext.KindInvalid || raw.Kind() == jsontext.KindNull {
		return 0, nil
	}
	if raw.Kind() != jsontext.KindNumber {
		return 0, fmt.Errorf("%s: expected a JSON number, got %s", name, jsonKind(raw))
	}
	value, err := memberInt(raw)
	if err != nil {
		return 0, wrap(name, err)
	}
	return value, nil
}

// maxUint64Exclusive is 2^64 — the first value past uint64. sizeString compares
// its float fallback against it.
const maxUint64Exclusive = float64(1 << 64)

// sizeString renders a file entry's size as the decimal text GameFile.Size carries: a
// string value is taken verbatim, and any other value becomes its unsigned decimal text
// when it is representable as one. A negative value, a non-integral number and a
// boolean have no unsigned form and render as "" — a missing size must not turn into a
// plausible-looking number.
//
// The member is a JSON number, so the token form answers for a whole value in
// uint64 range and the float fallback covers a literal such as "1024.0".
func sizeString(v jsontext.Value) string {
	switch v.Kind() {
	case jsontext.KindString:
		tok, err := tokenOf(v)
		if err != nil {
			return ""
		}
		return tok.String()
	case jsontext.KindNumber:
		tok, err := tokenOf(v)
		if err != nil {
			return ""
		}
		if u, err := tok.Uint(); err == nil {
			return strconv.FormatUint(u, 10)
		}
		f, err := tok.Float()
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f < 0 || f != math.Trunc(f) || f >= maxUint64Exclusive {
			return ""
		}
		return strconv.FormatUint(uint64(f), 10)
	default:
		// A boolean, a container, a missing member and a null all have no
		// unsigned size.
		return ""
	}
}
