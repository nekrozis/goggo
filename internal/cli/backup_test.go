package cli

import (
	"bytes"
	"net/url"
	"strings"
	"testing"

	"github.com/nekrozis/goggo/internal/config"
	"github.com/nekrozis/goggo/internal/core"
)

// TestBackupDownloadSpecsEquality verifies that `backup download <game> <file-id>`
// and the shorthand `backup download <game>/<file-id>` produce the exact same file specs.
func TestBackupDownloadSpecsEquality(t *testing.T) {
	inv2 := mustParse(t, "backup", "download", "homm5", "en1installer0")
	if len(inv2.args) != 2 {
		t.Fatalf("expected 2 args, got %v", inv2.args)
	}
	spec2 := inv2.args[0] + "/" + inv2.args[1]

	invSlash := mustParse(t, "backup", "download", "homm5/en1installer0")
	if len(invSlash.args) != 1 {
		t.Fatalf("expected 1 arg, got %v", invSlash.args)
	}
	specSlash := invSlash.args[0]

	if spec2 != specSlash {
		t.Errorf("expected specs to match: %q vs %q", spec2, specSlash)
	}
	if spec2 != "homm5/en1installer0" {
		t.Errorf("unexpected spec format: %q", spec2)
	}

	// Also check DLC file spec: game dlc/123 vs game/dlc/123
	invDLC := mustParse(t, "backup", "download", "homm5", "hof_dlc/45223")
	specDLC := invDLC.args[0] + "/" + invDLC.args[1]
	invDLCSlash := mustParse(t, "backup", "download", "homm5/hof_dlc/45223")
	if specDLC != invDLCSlash.args[0] {
		t.Errorf("expected DLC specs to match: %q vs %q", specDLC, invDLCSlash.args[0])
	}
}

// TestBackupDownloadMultiFileSpecs verifies that specifying multiple file selectors
// correctly constructs the list of specs under the single target game.
func TestBackupDownloadMultiFileSpecs(t *testing.T) {
	inv := mustParse(t, "backup", "download", "homm5", "en1installer0", "45223", "en1patch0")
	if len(inv.args) != 4 {
		t.Fatalf("expected 4 args (game + 3 files), got %d", len(inv.args))
	}
	game := inv.args[0]
	if game != "homm5" {
		t.Errorf("expected game homm5, got %s", game)
	}
	var specs []string
	for _, f := range inv.args[1:] {
		specs = append(specs, game+"/"+f)
	}
	want := []string{"homm5/en1installer0", "homm5/45223", "homm5/en1patch0"}
	if len(specs) != len(want) {
		t.Fatalf("specs len %d != %d", len(specs), len(want))
	}
	for i := range specs {
		if specs[i] != want[i] {
			t.Errorf("spec[%d] = %q, want %q", i, specs[i], want[i])
		}
	}
}

// TestBackupDownloadTypeOptionParsing asserts that --type parses the category
// TestWebsiteDownloadRequestBuilder is the DEFECT-TYPE1 wiring guard: the
// builder is the one carrier from --type intent to the core request.
// Parse-level assertions alone let the dead-write defect slip through - they
// checked that typeMask was set and never that anything consumed it.
func TestWebsiteDownloadRequestBuilder(t *testing.T) {
	inv := mustParse(t, "backup", "download", "game1", "--type", "extras")
	req := websiteDownloadRequest(inv)
	if req.Include == nil {
		t.Fatal("--type extras: Include = nil, want the request to carry the mask")
	}
	if *req.Include != config.GFExtra {
		t.Errorf("--type extras: Include = 0x%x, want GFExtra 0x%x", *req.Include, config.GFExtra)
	}
	if len(req.Products) != 1 || req.Products[0] != "game1" {
		t.Errorf("Products = %v, want [game1]", req.Products)
	}

	// nil means the configured mask: a default run must carry no intent.
	inv = mustParse(t, "backup", "download", "game1")
	if got := websiteDownloadRequest(inv); got.Include != nil {
		t.Error("no --type: Include != nil, want nil (configured mask)")
	}
}

// into the corresponding bitmask, and supports multiple values and aliases.
func TestBackupDownloadTypeOptionParsing(t *testing.T) {
	// 1. Single category: installers
	inv := mustParse(t, "backup", "download", "game1", "--type", "installers")
	if !inv.typeSet || inv.typeMask != config.GFInstaller {
		t.Errorf("expected GFInstaller mask (0x%x), got 0x%x", config.GFInstaller, inv.typeMask)
	}

	// 2. Single category: extras
	inv = mustParse(t, "backup", "download", "game1", "--type", "extras")
	if !inv.typeSet || inv.typeMask != config.GFExtra {
		t.Errorf("expected GFExtra mask (0x%x), got 0x%x", config.GFExtra, inv.typeMask)
	}

	// 3. Comma-separated: installers,extras
	inv = mustParse(t, "backup", "download", "game1", "--type", "installers,extras")
	want := config.GFInstaller | config.GFExtra
	if !inv.typeSet || inv.typeMask != want {
		t.Errorf("expected installers+extras mask (0x%x), got 0x%x", want, inv.typeMask)
	}

	// 4. Multiple --type flags (bitmask OR)
	inv = mustParse(t, "backup", "download", "game1", "--type", "installers", "--type", "patches")
	want = config.GFInstaller | config.GFPatch
	if !inv.typeSet || inv.typeMask != want {
		t.Errorf("expected installers+patches mask (0x%x), got 0x%x", want, inv.typeMask)
	}

	// 5. Invalid --type value: must fail with usage error
	err := mustUsageError(t, "backup", "download", "game1", "--type", "invalid_cat")
	if !strings.Contains(err.Error(), "invalid value for --type") {
		t.Errorf("expected invalid value error, got: %v", err)
	}
}

// TestBackupDownloadMutualExclusion locks the mutual exclusion between
// file selectors and --type, as well as --type and --include/--exclude.
func TestBackupDownloadMutualExclusion(t *testing.T) {
	// 1. File selector + --type -> REFUSED
	err := mustUsageError(t, "backup", "download", "game1", "45223", "--type", "extras")
	if !strings.Contains(err.Error(), "cannot combine file selectors with --type") {
		t.Errorf("expected mutual exclusion error, got: %v", err)
	}

	// 2. Shorthand file selector + --type -> REFUSED
	err = mustUsageError(t, "backup", "download", "game1/45223", "--type", "extras")
	if !strings.Contains(err.Error(), "cannot combine file selectors with --type") {
		t.Errorf("expected mutual exclusion error for shorthand, got: %v", err)
	}

	// 3. --type + --include -> REFUSED
	err = mustUsageError(t, "backup", "download", "game1", "--type", "extras", "--include", "i")
	if !strings.Contains(err.Error(), "cannot combine --type with --include/--exclude") {
		t.Errorf("expected mutual exclusion error for --include, got: %v", err)
	}
}

// TestBackupDownloadNeedsAGame locks the arity error when no arguments are provided.
func TestBackupDownloadNeedsAGame(t *testing.T) {
	err := mustUsageError(t, "backup", "download")
	if err == nil || !strings.Contains(err.Error(), "backup download needs a game") {
		t.Errorf("bare backup download = %v, want 'backup download needs a game'", err)
	}
}

// TestBackupDownloadExecutionEdgeCases exercises unknown file selectors
// and mixed valid/unknown selectors against the simulated environment.
func TestBackupDownloadExecutionEdgeCases(t *testing.T) {
	f := newSentinelFixture(t)
	target, err := url.Parse(f.URL)
	if err != nil {
		t.Fatal(err)
	}
	deps := core.Dependencies{HTTPTransport: &sentinelTransport{target: target}}

	// 1. Single unknown file selector: must fail with non-zero exit code and clear message.
	var stdout, stderr bytes.Buffer
	code := runWithDeps([]string{"backup", "download", "sentinel_game", "no_such_file_id"}, strings.NewReader(""), &stdout, &stderr, deps)
	if code == 0 {
		t.Errorf("expected non-zero exit for unknown selector, got 0")
	}
	if !strings.Contains(stderr.String(), "Failed to find file info") && !strings.Contains(stderr.String(), "no_such_file_id") {
		t.Errorf("expected failure message mentioning unknown id, got stderr:\n%s", stderr.String())
	}

	// 2. Mixed valid and unknown file selectors:
	// base.exe is a valid selector in sentinelProductDoc; invalid_id is unknown.
	// Must fail with non-zero exit code, reporting the failed selector.
	stderr.Reset()
	stdout.Reset()
	f.setDownload("http://127.0.0.1:1/nonexistent") // make downlink transfer fail cleanly if reached
	code = runWithDeps([]string{"backup", "download", "sentinel_game", "base.exe", "invalid_id"}, strings.NewReader(""), &stdout, &stderr, deps)
	if code == 0 {
		t.Errorf("expected non-zero exit for mixed valid/unknown selectors, got 0")
	}
	if !strings.Contains(stderr.String(), "Failed to find file info") && !strings.Contains(stderr.String(), "invalid_id") {
		t.Errorf("expected failure message mentioning invalid_id, got stderr:\n%s", stderr.String())
	}
}
