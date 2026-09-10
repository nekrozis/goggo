// Package gamedetails is the pure data layer of the website download face:
// the GameFile/GameDetails model, the priority and type filters, and the
// filepath template derivation (gamedetails.h, gamedetails.cpp). It performs
// no I/O of its own — the JSON conversion (S-GD2), the batch orchestration
// (S-GD3) and the command wiring (S-GD4) consume it.
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

	SerialsFilepath   string
	LogoFilepath      string
	IconFilepath      string
	ChangelogFilepath string
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

// GetGameFileVector collects every file of this product without the DLC
// subtree (gamedetails.cpp:233-253).
func (gd *GameDetails) GetGameFileVector() []GameFile {
	vector := make([]GameFile, 0, len(gd.Installers)+len(gd.Extras)+len(gd.Patches)+len(gd.LanguagePacks))
	vector = append(vector, gd.Installers...)
	vector = append(vector, gd.Extras...)
	vector = append(vector, gd.Patches...)
	vector = append(vector, gd.LanguagePacks...)
	return vector
}

// GetGameFileVectorFiltered collects the files whose type matches the mask
// (gamedetails.cpp:255-266).
func (gd *GameDetails) GetGameFileVectorFiltered(typeMask uint32) []GameFile {
	vector := make([]GameFile, 0, len(gd.Installers)+len(gd.Extras)+len(gd.Patches)+len(gd.LanguagePacks))
	vector = append(vector, filterWithType(gd.Installers, typeMask)...)
	vector = append(vector, filterWithType(gd.Extras, typeMask)...)
	vector = append(vector, filterWithType(gd.Patches, typeMask)...)
	vector = append(vector, filterWithType(gd.LanguagePacks, typeMask)...)
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

// MakeFilepaths derives every filepath of the tree: the six metadata paths per
// product (named with the gamename so DLCs cannot overwrite base-game files),
// then the four file vectors, recursing into the DLCs with their own names
// (gamedetails.cpp:80-168).
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

	makeVectorPaths(gd.Installers, dirConf)
	makeVectorPaths(gd.Extras, dirConf)
	makeVectorPaths(gd.Patches, dirConf)
	makeVectorPaths(gd.LanguagePacks, dirConf)

	for i := range gd.DLCs {
		dlc := &gd.DLCs[i]
		dlc.SerialsFilepath = gd.makeCustomFilepath("serials_"+dlc.Gamename+".txt", dirConf)
		dlc.LogoFilepath = gd.makeCustomFilepath("logo_"+dlc.Gamename+logoExt, dirConf)
		dlc.IconFilepath = gd.makeCustomFilepath("icon_"+dlc.Gamename+iconExt, dirConf)
		dlc.ChangelogFilepath = gd.makeCustomFilepath("changelog_"+dlc.Gamename+".html", dirConf)

		makeVectorPaths(dlc.Installers, dirConf)
		makeVectorPaths(dlc.Extras, dirConf)
		makeVectorPaths(dlc.Patches, dirConf)
		makeVectorPaths(dlc.LanguagePacks, dirConf)
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
