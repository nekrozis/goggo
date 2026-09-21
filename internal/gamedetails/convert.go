package gamedetails

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"fmt"
	"io"
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

// rawProduct models the product document root for unmarshaling.
type rawProduct struct {
	Slug         jsontext.Value   `json:"slug"`
	ID           jsontext.Value   `json:"id"`
	Title        string           `json:"title"`
	Changelog    string           `json:"changelog"`
	Images       rawImages        `json:"images"`
	Downloads    rawDownloads     `json:"downloads"`
	ExpandedDLCs []rawExpandedDLC `json:"expanded_dlcs"`
}

// rawExpandedDLC models a DLC entry inside expanded_dlcs.
type rawExpandedDLC struct {
	Slug         jsontext.Value   `json:"slug"`
	ID           jsontext.Value   `json:"id"`
	Title        string           `json:"title"`
	Changelog    string           `json:"changelog"`
	Images       rawImages        `json:"images"`
	Downloads    rawDownloads     `json:"downloads"`
	ExpandedDLCs []rawExpandedDLC `json:"expanded_dlcs"`
}

type rawImages struct {
	Icon string `json:"icon"`
	Logo string `json:"logo"`
}

type rawDownloads struct {
	Installers    []rawFileGroup `json:"installers"`
	BonusContent  []rawFileGroup `json:"bonus_content"`
	Patches       []rawFileGroup `json:"patches"`
	LanguagePacks []rawFileGroup `json:"language_packs"`
}

type rawFileGroup struct {
	Name      string    `json:"name"`
	Version   string    `json:"version"`
	OS        string    `json:"os"`
	Language  string    `json:"language"`
	Count     uint64    `json:"count"`
	TotalSize uint64    `json:"total_size"`
	Files     []rawFile `json:"files"`
}

type rawFile struct {
	ID       jsontext.Value `json:"id"`
	Downlink string         `json:"downlink"`
	Size     jsontext.Value `json:"size"`
}

func readToken(v jsontext.Value) (jsontext.Token, error) {
	if len(v) == 0 {
		return jsontext.Token{}, io.ErrUnexpectedEOF
	}
	dec := jsontext.NewDecoder(bytes.NewReader(v))
	return dec.ReadToken()
}

func readProductID(v jsontext.Value) (string, error) {
	if len(v) == 0 {
		return "", nil
	}
	tok, err := readToken(v)
	if err != nil {
		return "", err
	}
	switch tok.Kind() {
	case jsontext.KindNull:
		return "", nil
	case jsontext.KindString, jsontext.KindNumber:
		return tok.String(), nil
	case jsontext.KindTrue:
		return "true", nil
	case jsontext.KindFalse:
		return "false", nil
	default:
		return "", fmt.Errorf("id: expected a scalar, got %s", tok.Kind())
	}
}

func readFileID(v jsontext.Value) (string, error) {
	return readProductID(v)
}

func readProductSlug(v jsontext.Value) (string, error) {
	if len(v) == 0 {
		return "", nil
	}
	tok, err := readToken(v)
	if err != nil {
		return "", err
	}
	switch tok.Kind() {
	case jsontext.KindNull:
		return "", nil
	case jsontext.KindString:
		return tok.String(), nil
	default:
		return "", fmt.Errorf("slug: expected a JSON string, got %s", tok.Kind())
	}
}

func readFileSize(v jsontext.Value) (uint64, bool, error) {
	if len(v) == 0 {
		return 0, false, nil
	}
	tok, err := readToken(v)
	if err != nil {
		return 0, false, err
	}
	switch tok.Kind() {
	case jsontext.KindNull:
		return 0, false, nil
	case jsontext.KindNumber:
		u, err := tok.Uint()
		if err != nil {
			return 0, false, nil
		}
		return u, true, nil
	case jsontext.KindString:
		s := strings.TrimSpace(tok.String())
		if s == "" {
			return 0, false, nil
		}
		u, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			return 0, false, nil
		}
		return u, true, nil
	case jsontext.KindTrue, jsontext.KindFalse:
		return 0, false, nil
	default:
		return 0, false, nil
	}
}

// ProductInfoToGameDetails converts one Galaxy product document bytes into the domain
// model: a field the conversion reads is validated against the JSON type it must
// have — absent is the zero value, present with the wrong shape is an error — and
// values are never coerced.
//
// owned is the set of owned product ids; an EMPTY set means no filtering.
func ProductInfoToGameDetails(ctx context.Context, raw []byte, cfg config.DownloadConfig,
	owned map[string]bool, resolve DownlinkResolver) (GameDetails, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return GameDetails{}, nil
	}
	var p rawProduct
	if err := jsonv2.Unmarshal(raw, &p); err != nil {
		return GameDetails{}, wrap("gamedetails", err)
	}
	gd, err := convertProduct(ctx, p, raw, cfg, owned, resolve)
	if err != nil {
		// Nothing half-built crosses the boundary: a caller that gets an error
		// gets the zero value, never a partial tree it could mistake for a
		// result.
		return GameDetails{}, err
	}
	return gd, nil
}

func convertProduct(ctx context.Context, p rawProduct, raw []byte, cfg config.DownloadConfig,
	owned map[string]bool, resolve DownlinkResolver) (GameDetails, error) {
	var gd GameDetails

	gamename, err := readProductSlug(p.Slug)
	if err != nil {
		return GameDetails{}, wrap("gamedetails", err)
	}
	gd.Gamename = gamename

	productID, err := readProductID(p.ID)
	if err != nil {
		return GameDetails{}, wrap("gamedetails", err)
	}
	gd.ProductID = productID
	gd.Title = p.Title
	gd.Changelog = p.Changelog

	icon := p.Images.Icon
	gd.Icon = httpsPrefix + icon
	logo := p.Images.Logo
	gd.Logo = strings.ReplaceAll(httpsPrefix+logo, logoNameInAPI, logoNameFinal)

	if cfg.SaveProductJSON {
		var rawVal any
		if err := json.Unmarshal(raw, &rawVal); err == nil {
			rendered, err := util.StyledJSON(rawVal)
			if err != nil {
				return GameDetails{}, wrap("gamedetails: product json", err)
			}
			gd.ProductJson = rendered
		}
	}

	for _, v := range []struct {
		gate   uint32
		spec   uint32
		key    string
		groups []rawFileGroup
		dst    *[]GameFile
	}{
		{config.GFInstaller, config.GFBaseInstaller, "installers", p.Downloads.Installers, &gd.Installers},
		{config.GFExtra, config.GFBaseExtra, "bonus_content", p.Downloads.BonusContent, &gd.Extras},
		{config.GFPatch, config.GFBasePatch, "patches", p.Downloads.Patches, &gd.Patches},
		{config.GFLangPack, config.GFBaseLangPack, "language_packs", p.Downloads.LanguagePacks, &gd.LanguagePacks},
	} {
		if cfg.Include&v.gate == 0 {
			continue
		}
		files, err := gameFiles(ctx, gamename, gd.Title, v.key, v.groups, v.spec, cfg, resolve)
		if err != nil {
			return GameDetails{}, err
		}
		*v.dst = files
	}

	if cfg.Include&config.GFDLC == 0 {
		return gd, nil
	}
	for i, dlc := range p.ExpandedDLCs {
		where := fmt.Sprintf("gamedetails: expanded_dlcs[%d]", i)
		id, err := readProductID(dlc.ID)
		if err != nil {
			return GameDetails{}, wrap(where, err)
		}
		if len(owned) > 0 && !owned[id] {
			continue
		}
		sub, err := convertDLC(ctx, dlc, cfg, owned, resolve)
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

func convertDLC(ctx context.Context, dlc rawExpandedDLC, cfg config.DownloadConfig,
	owned map[string]bool, resolve DownlinkResolver) (GameDetails, error) {
	var gd GameDetails

	gamename, err := readProductSlug(dlc.Slug)
	if err != nil {
		return GameDetails{}, wrap("gamedetails", err)
	}
	gd.Gamename = gamename

	productID, err := readProductID(dlc.ID)
	if err != nil {
		return GameDetails{}, wrap("gamedetails", err)
	}
	gd.ProductID = productID
	gd.Title = dlc.Title
	gd.Changelog = dlc.Changelog

	icon := dlc.Images.Icon
	gd.Icon = httpsPrefix + icon
	logo := dlc.Images.Logo
	gd.Logo = strings.ReplaceAll(httpsPrefix+logo, logoNameInAPI, logoNameFinal)

	if cfg.SaveProductJSON {
		rendered, err := util.StyledJSON(dlc)
		if err != nil {
			return GameDetails{}, wrap("gamedetails: product json", err)
		}
		gd.ProductJson = rendered
	}

	for _, v := range []struct {
		gate   uint32
		spec   uint32
		key    string
		groups []rawFileGroup
		dst    *[]GameFile
	}{
		{config.GFInstaller, config.GFBaseInstaller, "installers", dlc.Downloads.Installers, &gd.Installers},
		{config.GFExtra, config.GFBaseExtra, "bonus_content", dlc.Downloads.BonusContent, &gd.Extras},
		{config.GFPatch, config.GFBasePatch, "patches", dlc.Downloads.Patches, &gd.Patches},
		{config.GFLangPack, config.GFBaseLangPack, "language_packs", dlc.Downloads.LanguagePacks, &gd.LanguagePacks},
	} {
		if cfg.Include&v.gate == 0 {
			continue
		}
		files, err := gameFiles(ctx, gamename, gd.Title, v.key, v.groups, v.spec, cfg, resolve)
		if err != nil {
			return GameDetails{}, err
		}
		*v.dst = files
	}

	if cfg.Include&config.GFDLC == 0 {
		return gd, nil
	}
	for i, subDLC := range dlc.ExpandedDLCs {
		where := fmt.Sprintf("gamedetails: expanded_dlcs[%d]", i)
		id, err := readProductID(subDLC.ID)
		if err != nil {
			return GameDetails{}, wrap(where, err)
		}
		if len(owned) > 0 && !owned[id] {
			continue
		}
		sub, err := convertDLC(ctx, subDLC, cfg, owned, resolve)
		if err != nil {
			return GameDetails{}, err
		}
		sub.TitleBasegame = gd.Title
		sub.GamenameBasegame = gd.Gamename
		retypeDLC(&sub)
		if len(sub.Installers)+len(sub.Extras)+len(sub.Patches)+len(sub.LanguagePacks) == 0 {
			continue
		}
		gd.DLCs = append(gd.DLCs, sub)
	}

	return gd, nil
}

// gameFiles converts one downloads vector: the platform/language filter, the
// empty-node skip and the per-file resolution.
func gameFiles(ctx context.Context, gamename, title, label string, groups []rawFileGroup, typeValue uint32,
	cfg config.DownloadConfig, resolve DownlinkResolver) ([]GameFile, error) {
	var out []GameFile
	// Extras carry no platform or language and are exempt from both filters.
	isExtra := typeValue&config.GFBaseExtra != 0

	for i, info := range groups {
		name := info.Name
		version := info.Version

		platform, language := config.PlatformWindows, config.LangEN
		if !isExtra {
			osName := info.OS
			langName := info.Language
			platform = util.OptionValue(osName, config.Platforms, false)
			language = util.OptionValue(langName, config.Languages, false)
			if platform&cfg.InstallerPlatform == 0 || language&cfg.InstallerLanguage == 0 {
				continue
			}
		}

		if info.Count == 0 && info.TotalSize == 0 {
			continue
		}

		for j, file := range info.Files {
			where := fmt.Sprintf("gamedetails: %s[%d].files[%d]", label, i, j)
			id, err := readFileID(file.ID)
			if err != nil {
				return nil, wrap(where, err)
			}
			downlink := file.Downlink
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
				ID:                    id,
				Name:                  name,
				Path:                  resolved.Path,
				GalaxyDownlinkJSONURL: downlink,
				Version:               version,
				Title:                 title,
				Type:                  typeValue,
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
			sz, ok, err := readFileSize(file.Size)
			if err != nil {
				return nil, wrap(where, err)
			}
			if ok {
				gf.Size = strconv.FormatUint(sz, 10)
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

func wrap(where string, err error) error {
	return fmt.Errorf("%s: %w", where, err)
}
