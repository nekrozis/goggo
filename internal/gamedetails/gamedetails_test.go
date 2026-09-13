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
