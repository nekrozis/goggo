package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nekrozis/goggo/internal/config"
	"github.com/nekrozis/goggo/internal/core"
	"github.com/nekrozis/goggo/internal/model"
)

func run(t *testing.T, stdin string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = Run(args, strings.NewReader(stdin), &out, &errOut)
	return code, out.String(), errOut.String()
}

// TestRunHelpAndVersion covers the two meta answers, which run before any
// session work (D18). The help is generated from the parser's tables, so it
// lists the command surface rather than a hand-kept option list.
func TestRunHelpAndVersion(t *testing.T) {
	for _, flag := range []string{"--help", "-h"} {
		code, out, errOut := run(t, "", flag)
		if code != 0 {
			t.Errorf("%s exit = %d, want 0", flag, code)
		}
		if !strings.HasPrefix(out, "Usage: ") {
			t.Errorf("%s output must start with the usage line, got %q", flag, out)
		}
		if !strings.Contains(out, "Usage: ") || !strings.Contains(out, "Commands:") {
			t.Errorf("%s output is missing the surface: %q", flag, out)
		}
		if errOut != "" {
			t.Errorf("%s stderr = %q", flag, errOut)
		}
	}

	// --version carries the full identity: our version plus the upstream
	// release this port tracks.
	for _, arg := range []string{"--version", "version"} {
		code, out, _ := run(t, "", arg)
		if code != 0 {
			t.Errorf("%s exit = %d, want 0", arg, code)
		}
		lines := strings.Split(strings.TrimSpace(out), "\n")
		wantVersion := config.VersionString
		wantCompat := config.UpstreamName + " compatibility: " + config.UpstreamCompatibilityVersion
		if len(lines) != 2 || lines[0] != wantVersion || lines[1] != wantCompat {
			t.Errorf("%s output = %q, want %q + %q", arg, out, wantVersion, wantCompat)
		}
	}

	// A bare invocation is a usage failure: it shows the surface and does not
	// look like a successful run.
	code, out, errOut := run(t, "")
	if code != 2 || out != "" || !strings.Contains(errOut, "Usage: ") {
		t.Errorf("bare invocation = %d/%q/%q, want a usage failure on stderr", code, out, errOut)
	}
}

// TestRunFailures locks the exit-code policy: nothing unimplemented or invalid
// may report success.
// TestRunFailures locks the failure contract (review CLI1 §8): anything the
// parser or the command tree refuses is a usage failure and exits 2, with the
// diagnostic on stderr and nothing on stdout. Removed upstream commands are
// unknown commands now, not "not implemented" options (D1/D14).
func TestRunFailures(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"unknown option", []string{"--nonsense"}, "unknown option"},
		{"unknown command", []string{"frobnicate"}, "unknown command"},
		{"removed command", []string{"download"}, "unknown command"},
		{"removed option", []string{"--download"}, "unknown option"},
		{"removed list option", []string{"--list", "details"}, "unknown option"},
		{"missing value", []string{"list", "games", "--tag"}, "requires a value"},
		{"invalid platform", []string{"install", "123", "--platform", "nope"}, "invalid value for --platform"},
		{"unaccepted option", []string{"list", "games", "--threads", "8"}, "not accepted"},
		{"missing game", []string{"install"}, "needs a game"},
		{"malformed target", []string{"install", "/2"}, "the game is empty"},
		{"show builds with a build", []string{"show", "builds", "123/2"}, "not a build"},
		{"bare invocation", nil, "Usage:"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, out, errOut := run(t, "", c.args...)
			if code != 2 {
				t.Errorf("exit = %d, want 2 (usage failure)", code)
			}
			if out != "" {
				t.Errorf("stdout = %q, want empty", out)
			}
			if !strings.Contains(errOut, c.want) {
				t.Errorf("stderr = %q, want it to contain %q", errOut, c.want)
			}
		})
	}
}

func TestRenderGames(t *testing.T) {
	items := []model.GameItem{
		{Name: "with-updates-new", Updates: 3, IsNew: true, DLCNames: []string{"dlc one", "dlc two"}},
		{Name: "with-updates-old", Updates: 1, IsNew: false},
		{Name: "new-only", IsNew: true},
		{Name: "plain"},
	}

	var buf bytes.Buffer
	if err := renderGames(&buf, items, true); err != nil {
		t.Fatalf("renderGames: %v", err)
	}
	want := strings.Join([]string{
		ansiNewGame + "with-updates-new [3]" + ansiReset,
		"+> dlc one",
		"+> dlc two",
		ansiUpdated + "with-updates-old [1]" + ansiReset,
		ansiNewGame + "new-only" + ansiReset,
		"plain",
		"",
	}, "\n")
	if buf.String() != want {
		t.Errorf("coloured output:\n got %q\nwant %q", buf.String(), want)
	}

	buf.Reset()
	if err := renderGames(&buf, items, false); err != nil {
		t.Fatalf("renderGames: %v", err)
	}
	if strings.Contains(buf.String(), "\033[") {
		t.Errorf("plain output must not contain escapes: %q", buf.String())
	}
	if !strings.Contains(buf.String(), "with-updates-new [3]\n") {
		t.Errorf("plain output = %q", buf.String())
	}
}

func TestRenderTags(t *testing.T) {
	var buf bytes.Buffer
	if err := renderTags(&buf, map[string]string{"zeta": "Z", "alpha": "A"}); err != nil {
		t.Fatalf("renderTags: %v", err)
	}
	if got, want := buf.String(), "alpha = A\nzeta = Z\n"; got != want {
		t.Errorf("tags = %q, want %q", got, want)
	}

	buf.Reset()
	if err := renderTags(&buf, nil); err != nil {
		t.Fatalf("renderTags: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("empty tags = %q", buf.String())
	}
}

// TestRenderWishlist locks the showWishlist layout (downloader.cpp:2530-2565),
// including the UTC release date.
func TestRenderWishlist(t *testing.T) {
	items := []model.WishlistItem{
		{
			Tags:                       []string{"Coming soon", "Discount"},
			Title:                      "Wanted",
			Currency:                   "$",
			Price:                      "12.500000$",
			DiscountPercent:            "50%",
			Discount:                   "1.25$",
			StoreCredit:                "0.500000$",
			URL:                        "https://www.gog.com/game/wanted",
			ReleaseDateTime:            1700000000, // 2023-Nov-14 22:13:20 UTC
			Platform:                   config.PlatformWindows | config.PlatformLinux,
			IsDiscounted:               true,
			IsBonusStoreCreditIncluded: true,
		},
		{Title: "Plain", Price: "0.000000$", URL: "https://www.gog.com/game/plain"},
	}

	var buf bytes.Buffer
	if err := renderWishlist(&buf, items); err != nil {
		t.Fatalf("renderWishlist: %v", err)
	}
	want := strings.Join([]string{
		"Wanted [Coming soon, Discount]",
		"\thttps://www.gog.com/game/wanted",
		"\tPlatforms: Windows, Linux",
		"\tRelease date: 2023-Nov-14 22:13:20",
		"\tPrice: 12.500000$ (-50% | -1.25$)",
		"\tStore credit: 0.500000$",
		"",
		"Plain",
		"\thttps://www.gog.com/game/plain",
		"\tPrice: 0.000000$",
		"",
		"",
	}, "\n")
	if buf.String() != want {
		t.Errorf("wishlist output:\n got %q\nwant %q", buf.String(), want)
	}
}

// TestGalaxyCommandArgument locks the split of the "<product id or
// The old argument helper (galaxyCommandArgument) is gone: splitting
// "<game>[/<build>]" now happens in the parser, and its shape rules are locked
// by TestParseTargetErrors and TestParseShowCommands in options_test.go.

// TestRunMalformedTargetFailsOffline locks that a malformed target fails before
// any session work: the parser answers, so no directory is created and no
// request is made (review CLI1 §6 — the argument is the parser's business now).
func TestRunMalformedTargetFailsOffline(t *testing.T) {
	for _, args := range [][]string{
		{"show", "builds", "/"},
		{"install", "/"},
		{"show", "cdns", "1/2/3"},
	} {
		code, out, errOut := run(t, "", args...)
		if code != 2 {
			t.Errorf("run(%v) exit = %d, want 2", args, code)
		}
		if out != "" {
			t.Errorf("run(%v) stdout = %q, want empty", args, out)
		}
		if !strings.Contains(errOut, "invalid target") {
			t.Errorf("run(%v) stderr = %q, want the argument error", args, errOut)
		}
	}
}

// TestRunHelpListsTheCommandSurface keeps the interim help in step with the
// parser: every command and every shared option the CLI accepts is listed, and
// nothing it removed is (review D14 — the help shows the supported surface).
func TestRunHelpListsTheCommandSurface(t *testing.T) {
	_, out, _ := run(t, "", "--help")
	for _, want := range []string{
		"auth", "login", "logout", "status",
		"list", "games", "tags", "wishlist",
		"show", "builds", "manifest", "cdns",
		"install", "verify", "orphans", "check", "remove",
		"help", "version",
		// The root topic shows the shared options; a command's own options
		// (--threads, --directory, ...) belong to its topic.
		"--verbose", "--no-color", "--no-unicode", "--unit-format",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("--help output does not mention %s: %q", want, out)
		}
	}
	for _, gone := range []string{"--galaxy-install", "--check-orphans", "--download", "--repair"} {
		if strings.Contains(out, gone) {
			t.Errorf("--help still advertises the removed %s: %q", gone, out)
		}
	}
}

// TestRunHelpForACommandAndRemovedCommands locks the two meta answers that do
// not need a session: a command topic renders from the tree, and a removed
// command is unknown (with the migration hint when one exists).
func TestRunHelpForACommandAndRemovedCommands(t *testing.T) {
	if code, out, _ := run(t, "", "help", "install"); code != 0 || !strings.Contains(out, "install") {
		t.Errorf("help install = %d/%q, want the topic", code, out)
	}
	if code, out, _ := run(t, "", "install", "-h"); code != 0 || !strings.Contains(out, "--platform") {
		t.Errorf("install -h = %d/%q, want the command's options", code, out)
	}
	if code, _, errOut := run(t, "", "repair"); code != 2 || !strings.Contains(errOut, "unknown command") {
		t.Errorf("removed command = %d/%q, want a usage failure", code, errOut)
	}
	if code, _, errOut := run(t, "", "--galaxy-install", "123"); code != 2 || !strings.Contains(errOut, "hint: use") {
		t.Errorf("removed option = %d/%q, want the hint", code, errOut)
	}
}

// The install argument is parsed by the same target rules as the show commands,
// so its malformed cases are covered by TestRunMalformedTargetFailsOffline.

// TestRenderBuilds locks the listing line (downloader.cpp:4906-4913).
func TestRenderBuilds(t *testing.T) {
	var buf bytes.Buffer
	rows := []core.BuildRow{
		{Index: 0, VersionName: "1.0.2", DatePublished: "2024-03-02", Generation: 2, BuildID: "b-new"},
		{Index: 1, VersionName: "1.0.1", DatePublished: "2024-01-05", Generation: 1, BuildID: "b-old"},
	}
	if err := renderBuilds(&buf, rows); err != nil {
		t.Fatalf("renderBuilds: %v", err)
	}
	want := "0: Version 1.0.2 - 2024-03-02 (Gen 2) (Build id: b-new)\n" +
		"1: Version 1.0.1 - 2024-01-05 (Gen 1) (Build id: b-old)\n"
	if buf.String() != want {
		t.Errorf("got %q\nwant %q", buf.String(), want)
	}

	buf.Reset()
	if err := renderBuilds(&buf, nil); err != nil {
		t.Fatalf("renderBuilds(nil): %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("empty listing = %q, want nothing", buf.String())
	}
}

// TestRenderManifest locks the three properties the C++ StyledStreamWriter
// output has: tab indentation, keys in byte order and no HTML escaping. A user
// pasting the document into a tool must find the URLs intact, so the & of a
// query string may not become \u0026.
func TestRenderManifest(t *testing.T) {
	var buf bytes.Buffer
	doc := map[string]any{
		"z": "https://cdn.gog.com/x?a=1&b=<2>",
		"a": map[string]any{"items": []any{float64(2), true}},
	}
	if err := renderManifest(&buf, doc); err != nil {
		t.Fatalf("renderManifest: %v", err)
	}
	want := "{\n" +
		"\t\"a\": {\n" +
		"\t\t\"items\": [\n" +
		"\t\t\t2,\n" +
		"\t\t\ttrue\n" +
		"\t\t]\n" +
		"\t},\n" +
		"\t\"z\": \"https://cdn.gog.com/x?a=1&b=<2>\"\n" +
		"}\n"
	if buf.String() != want {
		t.Errorf("got %q\nwant %q", buf.String(), want)
	}
}

// TestRenderCDNNames locks the one-name-per-line output
// (downloader.cpp:4394-4395).
func TestRenderCDNNames(t *testing.T) {
	var buf bytes.Buffer
	if err := renderCDNNames(&buf, []string{"gog-cdn-fastly", "gog-cdn-cloudflare"}); err != nil {
		t.Fatalf("renderCDNNames: %v", err)
	}
	if want := "gog-cdn-fastly\ngog-cdn-cloudflare\n"; buf.String() != want {
		t.Errorf("got %q, want %q", buf.String(), want)
	}
}

// TestRenderNotice locks the stream split: the C++ source prints the support and
// generation messages to stdout and the argument-resolution failures to stderr.
func TestRenderNotice(t *testing.T) {
	var out, errOut bytes.Buffer

	renderNotice(&out, &errOut, core.Notice{})
	if out.Len() != 0 || errOut.Len() != 0 {
		t.Errorf("an empty notice wrote %q / %q, want nothing", out.String(), errOut.String())
	}

	renderNotice(&out, &errOut, core.Notice{Text: "Only generation 2 builds are supported currently"})
	if want := "Only generation 2 builds are supported currently\n"; out.String() != want {
		t.Errorf("stdout = %q, want %q", out.String(), want)
	}
	if errOut.Len() != 0 {
		t.Errorf("stderr = %q, want nothing for a stdout notice", errOut.String())
	}

	out.Reset()
	renderNotice(&out, &errOut, core.Notice{Text: "Didn't match any products", Err: true})
	if out.Len() != 0 {
		t.Errorf("stdout = %q, want nothing for a stderr notice", out.String())
	}
	if want := "Didn't match any products\n"; errOut.String() != want {
		t.Errorf("stderr = %q, want %q", errOut.String(), want)
	}
}
