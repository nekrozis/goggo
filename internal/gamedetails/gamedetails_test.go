package gamedetails

import (
	"strings"
	"testing"

	"github.com/nekrozis/goggo/internal/config"
)

// dirConf builds a DirectoryConfig with one subdirectory template per file
// class, the way the CLI defaults lay them out.
func testDirConf() config.DirectoryConfig {
	return config.DirectoryConfig{
		Directory:          "/install",
		SubDirectories:     true,
		GameSubdir:         "%gamename%",
		InstallersSubdir:   "%platform%",
		ExtrasSubdir:       "extras",
		PatchesSubdir:      "patches",
		LanguagePackSubdir: "langpacks",
		DLCSubdir:          "dlc",
	}
}

// baseFile is an installer-shaped file with sane defaults for the path tests.
func baseFile(gamename, path string, platform uint32) GameFile {
	return GameFile{
		Gamename: gamename,
		Path:     path,
		Title:    "Title of " + gamename,
		Platform: platform,
		Type:     config.GFBaseInstaller,
		Version:  "1.2",
	}
}

// TestMakeFilepathPlaceholders walks the placeholder table one entry at a
// time, plus the double-slash folding.
func TestMakeFilepathPlaceholders(t *testing.T) {
	conf := testDirConf()
	cases := []struct {
		name string
		gf   GameFile
		want string
	}{
		{
			"gamename and platform",
			baseFile("some_game", "bin/game.exe", config.PlatformWindows),
			"/install/some_game/windows/game.exe",
		},
		{
			"firstletter keeps a letter",
			baseFile("some_game", "bin/game.exe", config.PlatformWindows),
			"/install/some_game/windows/game.exe",
		},
		{
			"version placeholder",
			func() GameFile {
				gf := baseFile("g", "bin/%version%.txt", config.PlatformWindows)
				gf.Version = "3.14"
				return gf
			}(),
			"/install/g/windows/3.14.txt",
		},
		{
			"double slash folds to single",
			baseFile("g", "bin//game.exe", config.PlatformWindows),
			"/install/g/windows/game.exe",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := makeFilepath(tc.gf, conf); got != tc.want {
				t.Errorf("makeFilepath = %q, want %q", got, tc.want)
			}
		})
	}

	// The firstletter placeholder only renders when the template asks for
	// it; the digit rule maps a leading digit to "0". This needs its own
	// template, and mutating the shared one would leak into the other cases.
	t.Run("firstletter digit becomes 0", func(t *testing.T) {
		conf := testDirConf()
		conf.GameSubdir = "%gamename_firstletter%/%gamename%"
		got := makeFilepath(baseFile("7_zip_game", "bin/game.exe", config.PlatformWindows), conf)
		if got != "/install/0/7_zip_game/windows/game.exe" {
			t.Errorf("makeFilepath = %q, want the 0-prefixed path", got)
		}
	})
}

// TestMakeFilepathNoPlatform locks the no_platform rule: a file without a
// matching platform lands in the no_platform folder, but the metadata files
// named logo_/icon_/product_ never do.
func TestMakeFilepathNoPlatform(t *testing.T) {
	conf := testDirConf()

	// The adjacent "%gamename%/%platform%" template would trigger the
	// disappearing-platform special case instead, so the game subdirectory
	// here does not use the gamename placeholder.
	conf.GameSubdir = "games"
	got := makeFilepath(baseFile("g", "bin/game.exe", 0), conf)
	if !strings.Contains(got, "/no_platform/") {
		t.Errorf("path = %q, want the no_platform folder", got)
	}

	// The metadata special case: the platform renders empty instead.
	logo := GameFile{
		Gamename: "g",
		Path:     "/logo_g.jpg",
		Title:    "t",
		Type:     config.GFCustomBase,
	}
	if got := makeFilepath(logo, conf); strings.Contains(got, "no_platform") {
		t.Errorf("path = %q, want no no_platform for a metadata file", got)
	}
}

// TestMakeFilepathDLC locks the DLC behaviour: the subdirectory gains the DLC
// prefix, the templates use the base game's identity, and %dlcname% carries
// the DLC's own gamename.
func TestMakeFilepathDLC(t *testing.T) {
	conf := testDirConf()
	gf := GameFile{
		Gamename:         "dlc_game",
		GamenameBasegame: "base_game",
		Title:            "DLC Title",
		TitleBasegame:    "Base Title",
		Path:             "bin/game.exe",
		Platform:         config.PlatformLinux,
		Type:             config.GFDLCInstaller,
	}
	got := makeFilepath(gf, conf)
	for _, want := range []string{
		"/install/base_game/dlc/linux/game.exe",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("path = %q, want it to contain %q", got, want)
		}
	}
}

// TestFilterWithPrioritiesKeepsTheBestScore locks the scoring: with the priority
// "French then English" and "Linux then Windows", the French Linux file is the
// only entry at the best score, so only it survives.
//
// Renamed from TestFilterWithPrioritiesKeepsTies, which claimed a tie case it
// never built (S-GD1 review Δ-GD1-T1); the tie itself is covered by
// TestFilterWithPrioritiesKeepsEveryTie below.
func TestFilterWithPrioritiesKeepsTheBestScore(t *testing.T) {
	frLinux := GameFile{Language: config.LangFR, Platform: config.PlatformLinux, Type: config.GFBaseInstaller}
	enLinux := GameFile{Language: config.LangEN, Platform: config.PlatformLinux, Type: config.GFBaseInstaller}
	enWindows := GameFile{Language: config.LangEN, Platform: config.PlatformWindows, Type: config.GFBaseInstaller}

	langPriority := []uint32{config.LangFR, config.LangEN}                     // French ranks first
	platformPriority := []uint32{config.PlatformLinux, config.PlatformWindows} // Linux first

	gd := GameDetails{Installers: []GameFile{frLinux, enLinux, enWindows}}
	gd.FilterWithPriorities(platformPriority, langPriority)

	// frLinux: platform 0 + language 0 = 0; enLinux: 0 + 1 = 1; enWindows:
	// 1 + 1 = 2 — the best score is 0, so only the French Linux file stays.
	if len(gd.Installers) != 1 || gd.Installers[0].Language != config.LangFR {
		t.Errorf("installers = %+v, want only the French Linux file", gd.Installers)
	}
	if gd.Installers[0].Score != 0 {
		t.Errorf("score = %d, want the written-back 0", gd.Installers[0].Score)
	}
}

// TestFilterWithPrioritiesKeepsEveryTie is the tie case the name above used to
// claim (S-GD1 review Δ-GD1-T1): two entries sharing the best score must both
// survive while a worse one goes. The upstream comparison is "score <=
// bestScore", so every entry at the best score is kept, not just the first.
func TestFilterWithPrioritiesKeepsEveryTie(t *testing.T) {
	frWindows := GameFile{Language: config.LangFR, Platform: config.PlatformWindows, Type: config.GFBaseInstaller}
	enLinux := GameFile{Language: config.LangEN, Platform: config.PlatformLinux, Type: config.GFBaseInstaller}
	enWindows := GameFile{Language: config.LangEN, Platform: config.PlatformWindows, Type: config.GFBaseInstaller}

	langPriority := []uint32{config.LangFR, config.LangEN}
	platformPriority := []uint32{config.PlatformLinux, config.PlatformWindows}

	gd := GameDetails{Installers: []GameFile{frWindows, enLinux, enWindows}}
	gd.FilterWithPriorities(platformPriority, langPriority)

	// frWindows: 0 + 1 = 1; enLinux: 1 + 0 = 1; enWindows: 1 + 1 = 2.
	// The two entries tied at the best score both stay.
	if len(gd.Installers) != 2 {
		t.Fatalf("installers = %+v, want the two entries tied at the best score", gd.Installers)
	}
	for _, gf := range gd.Installers {
		if gf.Score != 1 {
			t.Errorf("score = %d for %+v, want 1", gf.Score, gf)
		}
		if gf.Language == config.LangEN && gf.Platform == config.PlatformWindows {
			t.Errorf("the worst-scoring entry survived: %+v", gf)
		}
	}
}

// TestFilterWithPrioritiesExtrasUntouched locks the upstream boundary: the
// extras vector never participates in the priority filter.
func TestFilterWithPrioritiesExtrasUntouched(t *testing.T) {
	extra := GameFile{Language: config.LangEN, Platform: config.PlatformWindows, Type: config.GFBaseExtra}
	gd := GameDetails{Extras: []GameFile{extra, extra}}
	gd.FilterWithPriorities([]uint32{config.PlatformLinux}, []uint32{config.LangFR})
	if len(gd.Extras) != 2 {
		t.Errorf("extras = %+v, want them untouched", gd.Extras)
	}
}

// TestFilterWithPrioritiesEmptyListsIsNoop locks the early return: with both
// priority lists empty nothing is scored and nothing is erased.
func TestFilterWithPrioritiesEmptyListsIsNoop(t *testing.T) {
	gd := GameDetails{Installers: []GameFile{{}, {}}}
	gd.FilterWithPriorities(nil, nil)
	if len(gd.Installers) != 2 {
		t.Errorf("installers = %d, want an untouched list", len(gd.Installers))
	}
}

// TestFilterWithType locks the type mask filter.
func TestFilterWithType(t *testing.T) {
	gd := GameDetails{
		Installers: []GameFile{{Type: config.GFBaseInstaller}, {Type: config.GFDLCInstaller}},
		Extras:     []GameFile{{Type: config.GFBaseExtra}},
	}
	got := gd.GetGameFileVectorFiltered(config.GFInstaller)
	if len(got) != 2 {
		t.Errorf("filtered = %d, want the two installers", len(got))
	}
	if got := gd.GetGameFileVectorFiltered(config.GFPatch); len(got) != 0 {
		t.Errorf("filtered patches = %d, want none", len(got))
	}
}

// TestFilterWithTypeInPlace locks the exported in-place filter: it removes the
// excluded entries from the four vectors of the product AND from those of every
// DLC, leaving the rest of the tree alone (gamedetails.cpp:268-281). It is not
// the collecting filter above — the tree changes.
func TestFilterWithTypeInPlace(t *testing.T) {
	installer := GameFile{Type: config.GFBaseInstaller}
	extra := GameFile{Type: config.GFBaseExtra}
	patch := GameFile{Type: config.GFBasePatch}
	langpack := GameFile{Type: config.GFBaseLangPack}
	dlcInstaller := GameFile{Type: config.GFDLCInstaller}

	gd := GameDetails{
		Installers:    []GameFile{installer, extra},
		Extras:        []GameFile{extra, extra},
		Patches:       []GameFile{patch},
		LanguagePacks: []GameFile{langpack},
		Gamename:      "some_game",
		DLCs: []GameDetails{{
			Installers: []GameFile{dlcInstaller, extra},
			Extras:     []GameFile{extra},
			Gamename:   "some_dlc",
		}},
	}

	// The mask takes the two installer kinds and the two language-pack kinds:
	// what is left is the base installer, the base language pack and the DLC
	// installer, in their own vectors.
	gd.FilterWithType(config.GFInstaller | config.GFLangPack)

	if len(gd.Installers) != 1 || gd.Installers[0].Type != config.GFBaseInstaller {
		t.Errorf("installers = %+v, want only the base installer", gd.Installers)
	}
	if len(gd.Extras) != 0 {
		t.Errorf("extras = %+v, want none: the mask has no extras bit", gd.Extras)
	}
	if len(gd.Patches) != 0 {
		t.Errorf("patches = %+v, want none: the mask has no patch bit", gd.Patches)
	}
	if len(gd.LanguagePacks) != 1 || gd.LanguagePacks[0].Type != config.GFBaseLangPack {
		t.Errorf("language packs = %+v, want the base language pack kept", gd.LanguagePacks)
	}
	if len(gd.DLCs) != 1 {
		t.Fatalf("dlcs = %d, want the subtree kept", len(gd.DLCs))
	}
	if len(gd.DLCs[0].Installers) != 1 || gd.DLCs[0].Installers[0].Type != config.GFDLCInstaller {
		t.Errorf("dlc installers = %+v, want only the DLC installer", gd.DLCs[0].Installers)
	}
	if len(gd.DLCs[0].Extras) != 0 {
		t.Errorf("dlc extras = %+v, want none", gd.DLCs[0].Extras)
	}
	if gd.DLCs[0].Gamename != "some_dlc" {
		t.Errorf("the subtree's own fields must survive: %+v", gd.DLCs[0])
	}

	// An empty mask is the extreme case: upstream erases everything.
	gd.FilterWithType(0)
	if len(gd.Installers)+len(gd.Extras)+len(gd.Patches)+len(gd.LanguagePacks) != 0 ||
		len(gd.DLCs[0].Installers)+len(gd.DLCs[0].Extras)+len(gd.DLCs[0].Patches)+len(gd.DLCs[0].LanguagePacks) != 0 {
		t.Error("an empty mask must erase every vector, DLC subtree included")
	}
}

// TestMakeCustomFilepath locks the custom metadata path: a base game's files
// carry the custom-base type and land under the game directory without a DLC
// segment.
func TestMakeCustomFilepath(t *testing.T) {
	gd := GameDetails{Gamename: "some_game", Title: "Some Title"}
	got := gd.makeCustomFilepath("serials.txt", testDirConf())
	if !strings.Contains(got, "/install/some_game/serials.txt") {
		t.Errorf("path = %q, want the custom base path", got)
	}
}

// TestMakeFilepathsCachesAndDLCNames locks the cache population: the metadata
// filepaths are named with the gamename (so a DLC cannot overwrite the base
// game's files) and the DLC subtree gets its own caches.
func TestMakeFilepathsCachesAndDLCNames(t *testing.T) {
	gd := GameDetails{
		Gamename: "base_game",
		Title:    "Base",
		Logo:     "https://example.com/logo_glx_logo.jpg",
		Installers: []GameFile{
			{Gamename: "base_game", Path: "bin/game.exe", Platform: config.PlatformWindows, Type: config.GFBaseInstaller},
		},
		DLCs: []GameDetails{{
			Gamename: "dlc_game",
			Title:    "DLC",
			Installers: []GameFile{
				{Gamename: "dlc_game", Path: "bin/dlc.exe", Platform: config.PlatformWindows, Type: config.GFDLCInstaller},
			},
		}},
	}
	gd.MakeFilepaths(testDirConf())

	if !strings.Contains(gd.LogoFilepath, "logo_base_game") {
		t.Errorf("logo filepath = %q, want the gamename prefix", gd.LogoFilepath)
	}
	if !strings.Contains(gd.LogoFilepath, ".jpg") {
		t.Errorf("logo filepath = %q, want the extension taken from the logo url", gd.LogoFilepath)
	}
	if !strings.Contains(gd.DLCs[0].SerialsFilepath, "serials_dlc_game.txt") {
		t.Errorf("dlc serials filepath = %q, want the dlc gamename prefix", gd.DLCs[0].SerialsFilepath)
	}
	if gd.DLCs[0].Installers[0].GetFilepath() == "" {
		t.Error("the dlc installer did not get a cached filepath")
	}
}

// --- GD4 §3.1: the recursive file vector is the single source of truth ---

// dlcFile is a DLC-shaped file with the fields the conversion fills.
func dlcFile(base, dlc, path string, typ uint32) GameFile {
	return GameFile{
		Gamename:         dlc,
		GamenameBasegame: base,
		Path:             path,
		Title:            "Title of " + dlc,
		TitleBasegame:    "Title of " + base,
		Type:             typ,
		Version:          "1.0",
	}
}

// TestGetGameFileVectorRecursiveDLC locks the traversal contract: base vectors
// in goggo order (installers, extras, patches, language packs — the upstream
// order swaps extras and patches; GD4 ruling keeps the current order, plan
// §9), then each DLC recursively, DLCs in declaration order, depth-first,
// stable, no sorting.
func TestGetGameFileVectorRecursiveDLC(t *testing.T) {
	nested := GameDetails{
		Installers: []GameFile{dlcFile("dlc_one", "dlc_one_one", "n.exe", config.GFDLCInstaller)},
	}
	gd := GameDetails{
		Installers:    []GameFile{baseFile("base", "i1.exe", config.PlatformWindows)},
		Extras:        []GameFile{{Gamename: "base", Path: "e1.zip", Type: config.GFBaseExtra}},
		Patches:       []GameFile{{Gamename: "base", Path: "p1.zip", Type: config.GFBasePatch}},
		LanguagePacks: []GameFile{{Gamename: "base", Path: "l1.zip", Type: config.GFBaseLangPack}},
		DLCs: []GameDetails{
			{
				Installers: []GameFile{dlcFile("base", "dlc_one", "d1.exe", config.GFDLCInstaller)},
				Extras:     []GameFile{dlcFile("base", "dlc_one", "d2.zip", config.GFDLCExtra)},
				DLCs:       []GameDetails{nested},
			},
			{
				Patches: []GameFile{dlcFile("base", "dlc_two", "d3.zip", config.GFDLCPatch)},
			},
		},
	}

	got := gd.GetGameFileVector()
	wantOrder := []string{"i1.exe", "e1.zip", "p1.zip", "l1.zip", "d1.exe", "d2.zip", "n.exe", "d3.zip"}
	if len(got) != len(wantOrder) {
		t.Fatalf("vector = %d files, want %d (%v)", len(got), len(wantOrder), got)
	}
	for i, want := range wantOrder {
		if got[i].Path != want {
			t.Errorf("vector[%d] = %q, want %q (depth-first stable order)", i, got[i].Path, want)
		}
	}

	// The filtered view must be exactly the mask-filtered complete vector —
	// same files, same relative order (gamedetails.cpp:258-268).
	filtered := gd.GetGameFileVectorFiltered(config.GFInstaller)
	if len(filtered) != 3 {
		t.Fatalf("filtered installers = %d, want the base plus two DLC installers", len(filtered))
	}
	for i, want := range []string{"i1.exe", "d1.exe", "n.exe"} {
		if filtered[i].Path != want {
			t.Errorf("filtered[%d] = %q, want %q", i, filtered[i].Path, want)
		}
	}
	if got := gd.GetGameFileVectorFiltered(config.GFPatch); len(got) != 2 {
		t.Errorf("filtered patches = %d, want p1.zip and d3.zip", len(got))
	}
	if got := gd.GetGameFileVectorFiltered(config.GFDLC); len(got) != 4 {
		t.Errorf("filtered dlc = %d, want every file of the DLC subtree", len(got))
	}
}

// TestMakeFilepathsRecursiveDestinations locks that every file the recursive
// vector can reach also gets a destination, and that the base and first-level
// DLC paths match the pre-GD4 rendering exactly.
func TestMakeFilepathsRecursiveDestinations(t *testing.T) {
	conf := testDirConf()
	nested := GameDetails{
		Gamename:         "dlc_one",
		GamenameBasegame: "base",
		Extras:           []GameFile{dlcFile("dlc_one", "dlc_one_one", "n.zip", config.GFDLCExtra)},
	}
	gd := GameDetails{
		Gamename:   "base",
		Installers: []GameFile{baseFile("base", "bin/i1.exe", config.PlatformWindows)},
		DLCs: []GameDetails{
			{
				Gamename:         "base",
				GamenameBasegame: "",
			},
		},
	}
	d := &gd.DLCs[0]
	d.Gamename = "dlc_one"
	d.GamenameBasegame = "base"
	d.Extras = []GameFile{dlcFile("base", "dlc_one", "d.zip", config.GFDLCExtra)}
	d.DLCs = []GameDetails{nested}

	gd.MakeFilepaths(conf)

	if got, want := gd.Installers[0].GetFilepath(), "/install/base/windows/i1.exe"; got != want {
		t.Errorf("base installer = %q, want %q (unchanged by GD4)", got, want)
	}
	// A first-level DLC extra: dlc subdir + extras subdir under the base
	// game's directory (the %gamename% placeholder renders the basegame).
	if got, want := d.Extras[0].GetFilepath(), "/install/base/dlc/extras/d.zip"; got != want {
		t.Errorf("dlc extra = %q, want %q", got, want)
	}
	// The nested DLC file gets a destination too: its %gamename% renders the
	// nesting parent, which is what the conversion fills in GamenameBasegame.
	if got, want := nested.Extras[0].GetFilepath(), "/install/dlc_one/dlc/extras/n.zip"; got != want {
		t.Errorf("nested dlc extra = %q, want %q", got, want)
	}
}
