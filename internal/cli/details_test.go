package cli

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/nekrozis/goggo/internal/blacklist"
	"github.com/nekrozis/goggo/internal/config"
	"github.com/nekrozis/goggo/internal/core"
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

// TestRenderGameDetailsTextDownlinkLine locks the record's text face: stdout,
// non-verbose, after the header block and before the vector sections; the
// DLC section carries its own line after the identity rows. A healthy tree
// prints no line at all (the golden above is the byte-identity proof).
func TestRenderGameDetailsTextDownlinkLine(t *testing.T) {
	games := []gamedetails.GameDetails{{
		Gamename: "g", ProductID: "1", Title: "G", Icon: "i", Serials: "KEY\n",
		Installers: []gamedetails.GameFile{{
			Gamename: "g", ID: "i1", Name: "Setup", Path: "/setup.exe", Size: "10",
			Platform: config.PlatformWindows, Language: config.LangEN, Type: config.GFBaseInstaller,
		}},
		Downlink: &gamedetails.DownlinkDiag{Attempts: 5, Failures: 3, Usable: 2, FirstError: "boom"},
		DLCs: []gamedetails.GameDetails{{
			Gamename: "g_dlc", ProductID: "2",
			Patches: []gamedetails.GameFile{{
				Gamename: "g_dlc", ID: "p1", Name: "Patch", Path: "/patch.exe", Size: "10",
				Platform: config.PlatformWindows, Language: config.LangEN, Type: config.GFDLCPatch,
			}},
			Downlink: &gamedetails.DownlinkDiag{Attempts: 2, Failures: 1, Usable: 1, FirstError: "dlc boom"},
		}},
	}}
	var out, errOut bytes.Buffer
	renderGameDetailsText(&out, &errOut, games, &blacklist.Blacklist{}, false)
	got := out.String()

	const baseLine = "downlink: 3 of 5 files failed to resolve (first error: boom)\n"
	if !strings.Contains(got, baseLine) {
		t.Fatalf("base record line missing:\n%s", got)
	}
	if i, j := strings.Index(got, "icon: i\n"), strings.Index(got, baseLine); j <= i {
		t.Errorf("record line must follow the header block:\n%s", got)
	}
	if j := strings.Index(got, "installers: "); j <= strings.Index(got, baseLine) {
		t.Errorf("record line must precede the vector sections:\n%s", got)
	}
	if !strings.Contains(got, "product id: 2\ndownlink: 1 of 2 files failed to resolve (first error: dlc boom)\n") {
		t.Errorf("DLC record line must follow the identity rows:\n%s", got)
	}
	if errOut.Len() != 0 {
		t.Errorf("stderr = %q, want nothing: the record is a fact, not a warning", errOut.String())
	}
}

// TestAnyFullFailure pins the exit verdict to the predicate at top level:
// a full failure fails the command, a partial record and a healthy tree do
// not, and a kept DLC's partial record alone never does.
func TestAnyFullFailure(t *testing.T) {
	full := gamedetails.GameDetails{Downlink: &gamedetails.DownlinkDiag{Attempts: 3, Failures: 3}}
	partial := gamedetails.GameDetails{Downlink: &gamedetails.DownlinkDiag{Attempts: 5, Failures: 3, Usable: 2}}
	healthy := gamedetails.GameDetails{}
	keptDLC := gamedetails.GameDetails{DLCs: []gamedetails.GameDetails{partial}}
	for _, tc := range []struct {
		name  string
		games []gamedetails.GameDetails
		want  bool
	}{
		{"full", []gamedetails.GameDetails{full}, true},
		{"partial", []gamedetails.GameDetails{partial}, false},
		{"healthy", []gamedetails.GameDetails{healthy}, false},
		{"kept dlc partial", []gamedetails.GameDetails{keptDLC}, false},
		{"mixed", []gamedetails.GameDetails{healthy, full}, true},
	} {
		if got := anyFullFailure(tc.games); got != tc.want {
			t.Errorf("%s: anyFullFailure = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// The downlink verdict at the command level: the diagnosis must reach the
// output and the exit code must follow it, in that order, on both formats.
// Unit-level render and predicate tests cannot see the ordering — a verdict
// checked before the render would print nothing and still exit 1.

// listDownlinkDoc is one product with two installer files, each resolving
// through its own downlink document.
const listDownlinkDoc = `{"id":555,"slug":"sentinel_game","title":"Sentinel Game",` +
	`"images":{"icon":"//images.gog.com/icon.png","logo":"//images.gog.com/logo.jpg"},` +
	`"downloads":{"installers":[{"name":"pack","version":"1.0","count":2,"total_size":20,` +
	`"files":[{"id":"good.exe","downlink":"https://api.gog.com/dl/good.exe","size":10},` +
	`{"id":"bad.exe","downlink":"https://api.gog.com/dl/bad.exe","size":10}],` +
	`"os":"windows","language":"en"}],"bonus_content":[],"patches":[],"language_packs":[]}}`

// listDownlinkDeps answers the documents one `backup list` run reads; the
// named downlink documents fail when the case asks for it. The transport is
// the sentinel's: the fixture only replaces the served documents.
func listDownlinkDeps(t *testing.T, fail map[string]bool) core.Dependencies {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bad := func(name string) bool { return fail[name] }
		switch r.URL.Path {
		case "/www/account":
			fmt.Fprint(w, "account")
		case "/www/user/data/games":
			fmt.Fprint(w, `{"owned":["555"]}`)
		case "/www/account/getFilteredProducts":
			fmt.Fprint(w, `{"page":1,"totalPages":1,"products":[{"id":"555","slug":"sentinel_game"}]}`)
		case "/products/555":
			fmt.Fprint(w, listDownlinkDoc)
		case "/dl/good.exe":
			if bad("good.exe") {
				http.Error(w, "fixture failure", http.StatusNotFound)
				return
			}
			fmt.Fprint(w, `{"downlink":"https://cdn.gog.com/games/sentinel_game/good.exe"}`)
		case "/dl/bad.exe":
			if bad("bad.exe") {
				http.Error(w, "fixture failure", http.StatusNotFound)
				return
			}
			fmt.Fprint(w, `{"downlink":"https://cdn.gog.com/games/sentinel_game/bad.exe"}`)
		default:
			http.Error(w, "fixture failure", http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	target, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse fixture URL: %v", err)
	}
	return core.Dependencies{HTTPTransport: &sentinelTransport{target: target}}
}

func TestRunListDetailsDownlinkVerdict(t *testing.T) {
	t.Run("text", func(t *testing.T) {
		for _, tc := range []struct {
			name       string
			fail       map[string]bool
			wantExit   int
			wantLine   string
			wantAbsent string
		}{
			{"healthy", nil, 0, "", "downlink:"},
			{"partial", map[string]bool{"bad.exe": true}, 0,
				"downlink: 1 of 2 files failed to resolve", ""},
			{"full", map[string]bool{"good.exe": true, "bad.exe": true}, 1,
				"downlink: 2 of 2 files failed to resolve (first error: ", ""},
		} {
			t.Run(tc.name, func(t *testing.T) {
				sentinelRoots(t, sentinelExpiry(false))
				var stdout, stderr bytes.Buffer
				code := runWithDeps([]string{"backup", "list", "sentinel_game"},
					strings.NewReader(""), &stdout, &stderr, listDownlinkDeps(t, tc.fail))
				if code != tc.wantExit {
					t.Errorf("exit = %d, want %d (stderr: %s)", code, tc.wantExit, stderr.String())
				}
				if tc.wantLine != "" && !strings.Contains(stdout.String(), tc.wantLine) {
					t.Errorf("stdout lacks %q:\n%s", tc.wantLine, stdout.String())
				}
				if tc.wantAbsent != "" && strings.Contains(stdout.String(), tc.wantAbsent) {
					t.Errorf("stdout carries %q on a healthy run:\n%s", tc.wantAbsent, stdout.String())
				}
			})
		}
	})

	t.Run("json", func(t *testing.T) {
		sentinelRoots(t, sentinelExpiry(false))
		var stdout, stderr bytes.Buffer
		code := runWithDeps([]string{"backup", "list", "sentinel_game", "--json"},
			strings.NewReader(""), &stdout, &stderr,
			listDownlinkDeps(t, map[string]bool{"good.exe": true, "bad.exe": true}))
		// The document was fully written AND the command failed: the verdict
		// followed the render, it did not replace it.
		if code != 1 {
			t.Errorf("exit = %d, want 1 (stderr: %s)", code, stderr.String())
		}
		for _, want := range []string{`"downlink_diag"`, `"attempts": 2`, `"failures": 2`,
			`"usable": 0`, `"full_failure": true`} {
			if !strings.Contains(stdout.String(), want) {
				t.Errorf("json output lacks %s:\n%s", want, stdout.String())
			}
		}
	})
}
