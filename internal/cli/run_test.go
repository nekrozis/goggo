package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nekrozis/goggo/internal/config"
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
