package cli

import (
	"bytes"
	"regexp"
	"strings"
	"testing"

	"github.com/nekrozis/goggo/internal/blacklist"
	"github.com/nekrozis/goggo/internal/config"
	"github.com/nekrozis/goggo/internal/gamedetails"
)

// detailsFixtureGame builds the two-level tree the text golden renders: an
// installer (updated, version, language) and an extra on the base game, and
// one patch on the DLC. The extra's derived path is what the blacklist test
// entry uses.
func detailsFixtureGame(t *testing.T) []gamedetails.GameDetails {
	t.Helper()
	conf := config.DirectoryConfig{
		Directory:          "/install",
		SubDirectories:     true,
		GameSubdir:         "%gamename%",
		ExtrasSubdir:       "extras",
		PatchesSubdir:      "patches",
		LanguagePackSubdir: "languagepacks",
		DLCSubdir:          "dlc/%dlcname%",
	}
	gd := gamedetails.GameDetails{
		Gamename: "g", ProductID: "1", Title: "G", Icon: "https://i/x.png", Serials: "KEY-1\n",
		Installers: []gamedetails.GameFile{{
			Gamename: "g", ID: "en1installer0", Name: "Installer", Path: "/setup.exe", Size: "10",
			Platform: config.PlatformWindows, Language: config.LangEN, Type: config.GFBaseInstaller,
			Version: "1.2", Updated: 1,
		}},
		Extras: []gamedetails.GameFile{{
			Gamename: "g", ID: "4242", Name: "Soundtrack", Path: "/snd.zip", Size: "20",
			Type: config.GFBaseExtra,
		}},
		DLCs: []gamedetails.GameDetails{{
			Gamename: "g_dlc", GamenameBasegame: "g", ProductID: "2",
			Patches: []gamedetails.GameFile{{
				Gamename: "g_dlc", GamenameBasegame: "g", ID: "en1patch0", Name: "Patch",
				Path: "/patch.exe", Size: "30", Language: config.LangEN, Type: config.GFDLCPatch,
			}},
		}},
	}
	gd.MakeFilepaths(conf)
	return []gamedetails.GameDetails{gd}
}

func detailsBlacklist(path string) *blacklist.Blacklist {
	bl := &blacklist.Blacklist{}
	// Entries are "R <regex>" lines; the path is quoted so the dots are
	// literal and the match is anchored to the whole destination.
	bl.Initialize([]string{"R ^" + regexp.QuoteMeta(path) + "$"})
	return bl
}

// TestRenderGameDetailsTextGolden locks the exact wording — down to the
// trailing spaces, the DLC vector order, the blacklisted-file skip and the
// verbose note on stderr.
func TestRenderGameDetailsTextGolden(t *testing.T) {
	games := detailsFixtureGame(t)
	bl := detailsBlacklist(games[0].Extras[0].GetFilepath())

	var out, errOut bytes.Buffer
	renderGameDetailsText(&out, &errOut, games, bl, true)
	got := out.String()

	for _, want := range []string{
		"gamename: g\n",
		"serials:\nKEY-1\n\n",
		"installers: \n",
		"\tid: en1installer0\n",
		"\tupdated: True\n",
		// OptionNameString answers with the DISPLAY name — "English", not "en".
		"\tlanguage: English\n",
		"\tversion: 1.2\n",
		"DLCs: \n",
		"DLC gamename: g_dlc\n",
		"\tpath: /patch.exe",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
	// Empty vectors print no header. A vector whose every file is blacklisted
	// STILL prints its header: the emptiness test is on the vector, and the
	// blacklist only skips the rows.
	for _, absent := range []string{"language packs: ", "patches: "} {
		if strings.Contains(got, absent) {
			t.Errorf("must not appear: %q in\n%s", absent, got)
		}
	}
	if !strings.Contains(errOut.String(), "skipped blacklisted file") {
		t.Errorf("verbose blacklist note missing: %q", errOut.String())
	}

	out.Reset()
	errOut.Reset()
	renderGameDetailsText(&out, &errOut, games, bl, false)
	if errOut.Len() != 0 {
		t.Errorf("non-verbose stderr = %q, want nothing", errOut.String())
	}
}

// TestSaveFlagAcceptance locks the three-leaf registration: download writes,
// list details/json gate the display, and download file refuses the flags
// outright.
func TestSaveFlagAcceptance(t *testing.T) {
	inv := mustParse(t, "download", "g", "--save-serials", "--save-product-json")
	if !inv.cfg.DownloadConfig.SaveSerials || !inv.cfg.DownloadConfig.SaveProductJSON {
		t.Error("download batch must accept the save flags")
	}
	inv = mustParse(t, "list", "details", "--save-changelogs")
	if !inv.cfg.DownloadConfig.SaveChangelogs {
		t.Error("list details must accept the save flags as display gates")
	}
	mustParse(t, "list", "json", "--save-game-details-json")
	for _, args := range [][]string{
		{"download", "file", "g/1", "--save-serials"},
		{"list", "games", "--save-serials"},
		{"install", "123", "--save-serials"},
	} {
		err := mustUsageError(t, args...)
		if !strings.Contains(err.Error(), "not accepted") {
			t.Errorf("%v = %v, want the per-command refusal", args, err)
		}
	}
}

// TestInfoThreadsAcceptance locks the four acquisition leaves and the value
// rule: positive integers only.
func TestInfoThreadsAcceptance(t *testing.T) {
	for _, args := range [][]string{
		{"download", "g"}, {"download", "file", "g/1"}, {"list", "details"}, {"list", "json"},
	} {
		inv := mustParse(t, append(args, "--info-threads", "8")...)
		if inv.cfg.InfoThreads != 8 {
			t.Errorf("%v --info-threads = %d, want 8", args, inv.cfg.InfoThreads)
		}
	}
	for _, bad := range []string{"0", "-1", "x"} {
		err := mustUsageError(t, "list", "details", "--info-threads", bad)
		if !strings.Contains(err.Error(), "info-threads") {
			t.Errorf("--info-threads %s = %v, want the value refusal", bad, err)
		}
	}
	err := mustUsageError(t, "list", "games", "--info-threads", "8")
	if !strings.Contains(err.Error(), "not accepted") {
		t.Errorf("list games must not accept --info-threads: %v", err)
	}
}

// TestListDetailsArityAndHelp locks the arity at the parse surface: zero
// arguments is legal (the whole account), arguments select, and the topic
// says so — with the usage line, the notes and the options the node itself
// declares.
func TestListDetailsArityAndHelp(t *testing.T) {
	inv := mustParse(t, "list", "details")
	if len(inv.args) != 0 || inv.cmd != cmdListDetails {
		t.Errorf("bare list details = %v/%v, want the whole-account default", inv.cmd, inv.args)
	}
	inv = mustParse(t, "list", "json", "a", "b")
	if len(inv.args) != 2 || inv.cmd != cmdListJSON {
		t.Errorf("list json a b = %v %v", inv.cmd, inv.args)
	}

	details := topicNode(t, "list", "details")
	_, topic, _ := run(t, "", "list", "details", "-h")
	if want := commandUsageLine(t, "list", "details"); !strings.Contains(topic, want) {
		t.Errorf("list details topic missing its usage line %q:\n%s", want, topic)
	}
	for _, note := range nodeNotes(t, "list", "details") {
		if !strings.Contains(topic, note) {
			t.Errorf("list details topic missing its note %q:\n%s", note, topic)
		}
	}
	for _, long := range nodeOptionLongs(details) {
		if !strings.Contains(topic, long) {
			t.Errorf("list details topic missing %s:\n%s", long, topic)
		}
	}
}
