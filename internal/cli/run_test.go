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

// TestRunHelpAndVersion covers the two paths answered before any session work.
func TestRunHelpAndVersion(t *testing.T) {
	code, out, errOut := run(t, "", "--help")
	if code != 0 {
		t.Errorf("--help exit = %d, want 0", code)
	}
	if !strings.Contains(out, config.VersionString) {
		t.Errorf("--help output must start with the version, got %q", out)
	}
	if !strings.Contains(out, "--list") || !strings.Contains(out, "--login") {
		t.Errorf("--help output is missing options: %q", out)
	}
	if errOut != "" {
		t.Errorf("stderr = %q", errOut)
	}

	// --version carries the full identity: our version plus the upstream
	// release this port tracks.
	code, out, _ = run(t, "", "--version")
	if code != 0 {
		t.Errorf("--version exit = %d, want 0", code)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	wantVersion := config.VersionString
	wantCompat := config.UpstreamName + " compatibility: " + config.UpstreamCompatibilityVersion
	if len(lines) != 2 || lines[0] != wantVersion || lines[1] != wantCompat {
		t.Errorf("--version output = %q, want %q + %q", out, wantVersion, wantCompat)
	}
}

// TestRunFailures locks the exit-code policy: nothing unimplemented or invalid
// may report success.
func TestRunFailures(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"unknown option", []string{"--nonsense"}, "unknown option"},
		{"positional", []string{"game"}, "unexpected argument"},
		{"missing value", []string{"--game"}, "requires a value"},
		{"invalid platform", []string{"--platform", "nope"}, "invalid value for --platform"},
		{"save-config", []string{"--save-config"}, "not implemented"},
		{"reset-config", []string{"--reset-config"}, "not implemented"},
		{"update-cache", []string{"--update-cache"}, "not implemented"},
		{"list details", []string{"--list", "details"}, "not implemented"},
		{"list json", []string{"--list", "json"}, "not implemented"},
		{"download", []string{"--download"}, "not implemented"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, out, errOut := run(t, "", c.args...)
			if code != 1 {
				t.Errorf("exit = %d, want 1", code)
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
// gamename>[/<build id or index>]" argument (main.cpp:840-845). The C++ source
// ignores anything past the second token, and reads past the end of an empty
// vector for an argument that produces no token at all.
func TestGalaxyCommandArgument(t *testing.T) {
	cases := []struct {
		value     string
		wantID    string
		wantBuild string
		wantErr   bool
	}{
		{value: ""},
		{value: "12345", wantID: "12345"},
		{value: "12345/2", wantID: "12345", wantBuild: "2"},
		{value: "Some Game/1.0", wantID: "Some Game", wantBuild: "1.0"},
		{value: "12345/2/3", wantID: "12345"},
		{value: "/", wantErr: true},
		{value: "//", wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.value, func(t *testing.T) {
			productID, buildID, err := galaxyCommandArgument(c.value, "--galaxy-show-builds")
			if c.wantErr {
				if err == nil {
					t.Fatalf("galaxyCommandArgument(%q) must fail", c.value)
				}
				return
			}
			if err != nil {
				t.Fatalf("galaxyCommandArgument(%q): %v", c.value, err)
			}
			if productID != c.wantID || buildID != c.wantBuild {
				t.Errorf("got %q/%q, want %q/%q", productID, buildID, c.wantID, c.wantBuild)
			}
		})
	}
}

// TestRunGalaxyArgumentError locks that a malformed Galaxy argument fails before
// any session work: no directory is created, no request is made.
func TestRunGalaxyArgumentError(t *testing.T) {
	code, out, errOut := run(t, "", "--galaxy-show-builds", "/")
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	if out != "" {
		t.Errorf("stdout = %q, want empty", out)
	}
	if !strings.Contains(errOut, "no product id") {
		t.Errorf("stderr = %q, want the argument error", errOut)
	}
}

// TestRunHelpListsGalaxyOptions keeps the help text in step with the parser: an
// option that runs but is not documented is a user-visible gap.
func TestRunHelpListsGalaxyOptions(t *testing.T) {
	_, out, _ := run(t, "", "--help")
	for _, want := range []string{
		"--galaxy-show-builds", "--galaxy-list-cdns", "--galaxy-builds-sort", "--galaxy-platform",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("--help output does not mention %s: %q", want, out)
		}
	}
}

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
