// Package gamedetails is the pure data layer of the website download face: the
// GameFile/GameDetails model, the priority and type filters, the filepath
// template derivation (gamedetails.h, gamedetails.cpp) and — since GD1 — the
// JSON conversion of a Galaxy product document into that model (convert.go).
//
// The conversion resolves each file entry's downlink through an injected
// DownlinkResolver, so this package still performs no I/O of its own: the
// network and the cache live with the caller (GD2 / S-GD3), and the command
// wiring follows in GD3 / S-GD4.
package gamedetails

import (
	"sort"
	"strings"

	"github.com/nekrozis/goggo/internal/config"
	"github.com/nekrozis/goggo/internal/util"
)

// GameDetails is one product's download face: the four file vectors, the DLC
// subtree and the metadata. The recursion mirrors the upstream structure — a
// DLC is a full GameDetails, not a flat entry (review D-GD4).
type GameDetails struct {
	Installers    []GameFile
	Extras        []GameFile
	Patches       []GameFile
	LanguagePacks []GameFile
	DLCs          []GameDetails

	Gamename         string
	GamenameBasegame string
	ProductID        string
	Title            string
	TitleBasegame    string
	Icon             string
	Serials          string
	Changelog        string
	Logo             string

	// GD5 output-face data: the rendered JSON documents the save-* flags
	// request, and the diagnostics the extraction refused to guess at.
	// ProductJson is rendered from the document acquisition already holds
	// (zero extra requests, GD5 ruling 5); GameDetailsJson is the rendered
	// per-game details document; SerialsDiag says WHY serials stayed empty
	// when the cdKey shape is unsupported (ruling 1 fail-closed);
	// MetadataDiag carries a failed details fetch (GD5 Gate 2: a swallowed
	// fetch would hide a failure from the exit code).
	ProductJson     string
	GameDetailsJson string
	SerialsDiag     string
	MetadataDiag    string

	SerialsFilepath         string
	LogoFilepath            string
	IconFilepath            string
	ChangelogFilepath       string
	GameDetailsJSONFilepath string
	ProductJsonFilepath     string
}

// FilterWithPriorities removes the entries whose platform/language rank worse
// than the best-ranked entry, keeping every tie (gamedetails.cpp:19-34).
// Extras are intentionally not filtered — upstream filters installers, patches
// and languagepacks only, including inside the DLC subtree. With both priority
// lists empty the call is a no-op.
func (gd *GameDetails) FilterWithPriorities(platformPriority, languagePriority []uint32) {
	if len(platformPriority) == 0 && len(languagePriority) == 0 {
		return
	}

	// The filter erases in place (the returned slice shares the backing
	// array), so every vector is written back.
	gd.Installers = filterListWithPriorities(gd.Installers, platformPriority, languagePriority)
	gd.Patches = filterListWithPriorities(gd.Patches, platformPriority, languagePriority)
	gd.LanguagePacks = filterListWithPriorities(gd.LanguagePacks, platformPriority, languagePriority)
	for i := range gd.DLCs {
		gd.DLCs[i].Installers = filterListWithPriorities(gd.DLCs[i].Installers, platformPriority, languagePriority)
		gd.DLCs[i].Patches = filterListWithPriorities(gd.DLCs[i].Patches, platformPriority, languagePriority)
		gd.DLCs[i].LanguagePacks = filterListWithPriorities(gd.DLCs[i].LanguagePacks, platformPriority, languagePriority)
	}
}

// filterListWithPriorities scores each entry — the sum of the first matching
// priority index for platform and language, lower is better — writes it back
// into GameFile.Score, and erases everything above the best score, keeping all
// ties (gamedetails.cpp:35-79).
func filterListWithPriorities(list []GameFile, platformPriority, languagePriority []uint32) []GameFile {
	bestscore := -1
	for i := range list {
		list[i].Score = 0
		if len(platformPriority) != 0 {
			for p, prio := range platformPriority {
				if list[i].Platform&prio != 0 {
					list[i].Score += p
					break
				}
			}
		}
		if len(languagePriority) != 0 {
			for l, prio := range languagePriority {
				if list[i].Language&prio != 0 {
					list[i].Score += l
					break
				}
			}
		}
		if list[i].Score < bestscore || bestscore < 0 {
			bestscore = list[i].Score
		}
	}

	kept := list[:0]
	for i := range list {
		if list[i].Score <= bestscore {
			kept = append(kept, list[i])
		}
	}
	return kept
}

// GetGameFileVector collects every file of this product including the DLC
// subtree, depth-first in vector order (gamedetails.cpp:233-255). The
// concatenation order is goggo's own (installers, extras, patches, language
// packs — upstream swaps extras and patches; GD4 ruling keeps the current
// order and locks it with tests, plan §9). FilterWithPriorities and
// FilterWithType stay one level deep like upstream: acquisition never nests
// DLCs, so the recursion here is the single source of truth for consumers.
func (gd *GameDetails) GetGameFileVector() []GameFile {
	vector := make([]GameFile, 0, len(gd.Installers)+len(gd.Extras)+len(gd.Patches)+len(gd.LanguagePacks))
	vector = append(vector, gd.Installers...)
	vector = append(vector, gd.Extras...)
	vector = append(vector, gd.Patches...)
	vector = append(vector, gd.LanguagePacks...)
	for i := range gd.DLCs {
		vector = append(vector, gd.DLCs[i].GetGameFileVector()...)
	}
	return vector
}

// GetGameFileVectorFiltered collects the files whose type matches the mask.
// It filters the complete recursive vector — one source of truth, no second
// traversal (gamedetails.cpp:258-268).
func (gd *GameDetails) GetGameFileVectorFiltered(typeMask uint32) []GameFile {
	full := gd.GetGameFileVector()
	vector := make([]GameFile, 0, len(full))
	for i := range full {
		if full[i].Type&typeMask != 0 {
			vector = append(vector, full[i])
		}
	}
	return vector
}

// filterWithType keeps the entries matching the type mask
// (gamedetails.cpp:268-281).
func filterWithType(list []GameFile, typeMask uint32) []GameFile {
	var out []GameFile
	for i := range list {
		if list[i].Type&typeMask != 0 {
			out = append(out, list[i])
		}
	}
	return out
}

// FilterWithType drops the files the type mask excludes, in place, and does the
// same for the DLC subtree (gameDetails::filterWithType, gamedetails.cpp:268-281).
//
// It replaces the four vectors rather than collecting a new one — that is what
// makes it different from GetGameFileVectorFiltered, which gathers the matching
// files of a product and leaves the tree alone. Upstream descends one level into
// the DLCs, exactly as FilterWithPriorities does here.
//
// Acquisition calls it with the include mask (GD3); with a mask whose bits the
// conversion already gated each vector by, it removes nothing — it is called for
// upstream parity, and the mask semantics are what its own tests lock.
func (gd *GameDetails) FilterWithType(typeMask uint32) {
	gd.Installers = filterWithType(gd.Installers, typeMask)
	gd.Extras = filterWithType(gd.Extras, typeMask)
	gd.Patches = filterWithType(gd.Patches, typeMask)
	gd.LanguagePacks = filterWithType(gd.LanguagePacks, typeMask)
	for i := range gd.DLCs {
		dlc := &gd.DLCs[i]
		dlc.Installers = filterWithType(dlc.Installers, typeMask)
		dlc.Extras = filterWithType(dlc.Extras, typeMask)
		dlc.Patches = filterWithType(dlc.Patches, typeMask)
		dlc.LanguagePacks = filterWithType(dlc.LanguagePacks, typeMask)
	}
}

// MakeFilepaths derives every filepath of the tree: the metadata paths for
// the base game and each DLC (named with the gamename so DLCs cannot
// overwrite base-game files), and the file vectors of the whole subtree via
// the recursive helper (gamedetails.cpp:80-168).
func (gd *GameDetails) MakeFilepaths(dirConf config.DirectoryConfig) {
	logoExt := ".jpg"
	iconExt := ".png"
	if i := strings.LastIndex(gd.Logo, "."); i != -1 {
		logoExt = gd.Logo[i:]
	}
	if i := strings.LastIndex(gd.Icon, "."); i != -1 {
		iconExt = gd.Icon[i:]
	}

	gd.SerialsFilepath = gd.makeCustomFilepath("serials.txt", dirConf)
	gd.LogoFilepath = gd.makeCustomFilepath("logo_"+gd.Gamename+logoExt, dirConf)
	gd.IconFilepath = gd.makeCustomFilepath("icon_"+gd.Gamename+iconExt, dirConf)
	gd.ChangelogFilepath = gd.makeCustomFilepath("changelog_"+gd.Gamename+".html", dirConf)
	// The two GD5 JSON paths: game-details.json carries NO gamename prefix
	// (gamedetails.cpp:102) and belongs to the base game only; the product
	// json is named with the gamename (gamedetails.cpp:96,135).
	gd.GameDetailsJSONFilepath = gd.makeCustomFilepath("game-details.json", dirConf)
	gd.ProductJsonFilepath = gd.makeCustomFilepath("product_"+gd.Gamename+".json", dirConf)

	gd.makeVectorFilepaths(dirConf)

	for i := range gd.DLCs {
		dlc := &gd.DLCs[i]
		dlc.SerialsFilepath = gd.makeCustomFilepath("serials_"+dlc.Gamename+".txt", dirConf)
		dlc.LogoFilepath = gd.makeCustomFilepath("logo_"+dlc.Gamename+logoExt, dirConf)
		dlc.IconFilepath = gd.makeCustomFilepath("icon_"+dlc.Gamename+iconExt, dirConf)
		dlc.ChangelogFilepath = gd.makeCustomFilepath("changelog_"+dlc.Gamename+".html", dirConf)
		// A DLC gets a product json but no game-details.json
		// (gamedetails.cpp:135-141).
		dlc.ProductJsonFilepath = gd.makeCustomFilepath("product_"+dlc.Gamename+".json", dirConf)
	}
}

// makeVectorFilepaths derives the destination of every file vector of the
// whole subtree. Upstream renders the base and one DLC level flat
// (gamedetails.cpp:105-168); the recursion is GD4's approved superset that
// keeps paths consistent with the recursive file vector (plan §3.1/§9) —
// real products never nest DLCs, so behaviour at existing depths is unchanged.
func (gd *GameDetails) makeVectorFilepaths(dirConf config.DirectoryConfig) {
	makeVectorPaths(gd.Installers, dirConf)
	makeVectorPaths(gd.Extras, dirConf)
	makeVectorPaths(gd.Patches, dirConf)
	makeVectorPaths(gd.LanguagePacks, dirConf)
	for i := range gd.DLCs {
		gd.DLCs[i].makeVectorFilepaths(dirConf)
	}
}

// makeVectorPaths derives and stores the filepath of every entry in place.
func makeVectorPaths(list []GameFile, dirConf config.DirectoryConfig) {
	for i := range list {
		list[i].SetFilepath(makeFilepath(list[i], dirConf))
	}
}

// makeFilepath renders the local path of one file from the directory
// templates (gamedetails.cpp:294-418). The pipeline is order-sensitive:
// the type subdirectory, the DLC subdirectory, the game subdirectory, then
// the platform derivation, then the placeholder replacement in std::map
// (lexicographic) order, then the double-slash folding.
func makeFilepath(gf GameFile, dirConf config.DirectoryConfig) string {
	filename := gf.Path
	if i := strings.LastIndex(gf.Path, "/"); i != -1 {
		filename = gf.Path[i+1:]
	}

	var subdir string
	if dirConf.SubDirectories {
		switch {
		case gf.Type&config.GFInstaller != 0:
			subdir = dirConf.InstallersSubdir
		case gf.Type&config.GFExtra != 0:
			subdir = dirConf.ExtrasSubdir
		case gf.Type&config.GFPatch != 0:
			subdir = dirConf.PatchesSubdir
		case gf.Type&config.GFLangPack != 0:
			subdir = dirConf.LanguagePackSubdir
		}
		if gf.Type&config.GFDLC != 0 {
			subdir = dirConf.DLCSubdir + "/" + subdir
		}
	}
	if dirConf.GameSubdir != "" {
		subdir = dirConf.GameSubdir + "/" + subdir
	}

	gamename := gf.Gamename
	title := gf.Title
	dlcGamename := ""
	dlcTitle := ""
	if gf.Type&config.GFDLC != 0 {
		gamename = gf.GamenameBasegame
		title = gf.TitleBasegame
		dlcGamename = gf.Gamename
		dlcTitle = gf.Title
	}

	filepath := dirConf.Directory + "/" + subdir + "/" + filename

	// The platform name comes from the first table entry whose flags are all
	// present in the file's platform mask (downloader's PLATFORMS walk).
	platform := ""
	for _, o := range config.Platforms {
		if gf.Platform&o.ID == o.ID {
			platform = strings.ToLower(o.Name)
			break
		}
	}
	if platform == "" {
		if strings.Contains(filepath, "%gamename%/%platform%") {
			platform = ""
		} else {
			platform = "no_platform"
		}
	}

	// Metadata files never land in the no_platform folder.
	logoFilename := "/logo_" + gf.Gamename + ".jpg"
	iconFilename := "/icon_" + gf.Gamename + ".png"
	productJSONFilename := "/product_" + gf.Gamename + ".json"
	if strings.Contains(filepath, logoFilename) ||
		strings.Contains(filepath, iconFilename) ||
		strings.Contains(filepath, productJSONFilename) {
		platform = ""
	}

	gamenameFirstletter := ""
	if gamename != "" {
		if isDigit(gamename[0]) {
			gamenameFirstletter = "0"
		} else {
			gamenameFirstletter = gamename[:1]
		}
	}

	// The transformed gamename needs the transformations JSON (config.h:327),
	// which this build has not ported yet: the two placeholders render empty
	// (review D-GD5).
	gamenameTransformed := ""
	gamenameTransformedFirstletter := ""

	templates := [][2]string{
		{"%gamename%", gamename},
		{"%gamename_firstletter%", gamenameFirstletter},
		{"%title%", title},
		{"%title_stripped%", util.StrippedString(title)},
		{"%dlcname%", dlcGamename},
		{"%dlc_title%", dlcTitle},
		{"%dlc_title_stripped%", util.StrippedString(dlcTitle)},
		{"%platform%", platform},
		{"%gamename_transformed%", gamenameTransformed},
		{"%gamename_transformed_firstletter%", gamenameTransformedFirstletter},
		{"%version%", gf.Version},
	}
	// std::map iterates in key order; keep the same fixed order.
	sort.Slice(templates, func(a, b int) bool { return templates[a][0] < templates[b][0] })
	for _, t := range templates {
		filepath, _ = util.ReplaceAll(filepath, t[0], t[1])
	}

	filepath, _ = util.ReplaceAll(filepath, "//", "/")
	return filepath
}

// makeCustomFilepath renders a metadata file's path by reusing the regular
// filepath pipeline with a synthetic GameFile typed as custom base or custom
// DLC (gamedetails.cpp:419-437).
func (gd *GameDetails) makeCustomFilepath(filename string, dirConf config.DirectoryConfig) string {
	gf := GameFile{
		Gamename:         gd.Gamename,
		Path:             "/" + filename,
		Title:            gd.Title,
		GamenameBasegame: gd.GamenameBasegame,
		TitleBasegame:    gd.TitleBasegame,
	}
	if gf.GamenameBasegame == "" {
		gf.Type = config.GFCustomBase
	} else {
		gf.Type = config.GFCustomDLC
	}
	return makeFilepath(gf, dirConf)
}

// isDigit reports whether b is an ASCII decimal digit (std::isdigit's use
// upstream, applied to the first byte).
func isDigit(b byte) bool { return '0' <= b && b <= '9' }
