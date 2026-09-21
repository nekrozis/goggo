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

// TestSaveFlagAcceptance locks the save flags registration: backup download writes,
// backup list gates the display, and commands like list games refuse the flags
// outright.
func TestSaveFlagAcceptance(t *testing.T) {
	inv := mustParse(t, "backup", "download", "g", "--save-serials", "--save-product-json")
	if !inv.cfg.DownloadConfig.SaveSerials || !inv.cfg.DownloadConfig.SaveProductJSON {
		t.Error("backup download must accept the save flags")
	}
	inv = mustParse(t, "backup", "list", "--save-changelogs")
	if !inv.cfg.DownloadConfig.SaveChangelogs {
		t.Error("backup list must accept the save flags as display gates")
	}
	for _, args := range [][]string{
		{"list", "games", "--save-serials"},
		{"install", "123", "--save-serials"},
	} {
		err := mustUsageError(t, args...)
		if !strings.Contains(err.Error(), "not accepted") {
			t.Errorf("%v = %v, want the per-command refusal", args, err)
		}
	}
}

// TestInfoThreadsAcceptance locks the acquisition leaves and the value
// rule: positive integers only.
func TestInfoThreadsAcceptance(t *testing.T) {
	for _, args := range [][]string{
		{"backup", "download", "g"}, {"backup", "download", "g", "1"}, {"backup", "list"},
	} {
		inv := mustParse(t, append(args, "--info-threads", "8")...)
		if inv.cfg.InfoThreads != 8 {
			t.Errorf("%v --info-threads = %d, want 8", args, inv.cfg.InfoThreads)
		}
	}
	for _, bad := range []string{"0", "-1", "x"} {
		err := mustUsageError(t, "backup", "list", "--info-threads", bad)
		if !strings.Contains(err.Error(), "info-threads") {
			t.Errorf("--info-threads %s = %v, want the value refusal", bad, err)
		}
	}
	err := mustUsageError(t, "list", "games", "--info-threads", "8")
	if !strings.Contains(err.Error(), "not accepted") {
		t.Errorf("list games must not accept --info-threads: %v", err)
	}
}

// TestBackupListArityAndHelp locks the arity at the parse surface: zero
// arguments is legal (the whole account), arguments select, and the topic
// says so — with the usage line, the notes and the options the node itself
// declares.
func TestBackupListArityAndHelp(t *testing.T) {
	inv := mustParse(t, "backup", "list")
	if len(inv.args) != 0 || inv.cmd != cmdBackupList {
		t.Errorf("bare backup list = %v/%v, want the whole-account default", inv.cmd, inv.args)
	}
	inv = mustParse(t, "backup", "list", "a", "b")
	if len(inv.args) != 2 || inv.cmd != cmdBackupList {
		t.Errorf("backup list a b = %v %v", inv.cmd, inv.args)
	}

	details := topicNode(t, "backup", "list")
	_, topic, _ := run(t, "", "backup", "list", "-h")
	if want := commandUsageLine(t, "backup", "list"); !strings.Contains(topic, want) {
		t.Errorf("backup list topic missing its usage line %q:\n%s", want, topic)
	}
	for _, note := range nodeNotes(t, "backup", "list") {
		if !strings.Contains(topic, note) {
			t.Errorf("backup list topic missing its note %q:\n%s", note, topic)
		}
	}
	for _, long := range nodeOptionLongs(details) {
		if !strings.Contains(topic, long) {
			t.Errorf("backup list topic missing %s:\n%s", long, topic)
		}
	}
}
