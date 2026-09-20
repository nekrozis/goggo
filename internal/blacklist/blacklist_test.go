package blacklist

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestInitializeAndMatch locks the line format and the matching rule: flags
// before a space, 'R' required, matching an unanchored search.
func TestInitializeAndMatch(t *testing.T) {
	var b Blacklist
	diagnostics := b.Initialize([]string{
		"# a comment", // skipped
		"",            // skipped
		`R \.txt$`,    // any path containing .txt
		`R ^game/`,    // prefix-like, still a search
		`pR \.exe$`,   // 'p' is a no-op
		"z R\\.log$",  // unknown flag 'z', and then no 'R' at all
		"R",           // flags without an expression
		"R invalid [", // an invalid regexp is reported, not fatal
	})

	if !b.IsBlacklisted("C:/x/file.txt") {
		t.Error(".txt entry did not match")
	}
	if !b.IsBlacklisted("game/data/x") {
		t.Error("game/ entry did not match")
	}
	if !b.IsBlacklisted("setup.exe") {
		t.Error("the p-flagged entry did not match")
	}
	if b.IsBlacklisted("nope.bin") {
		t.Error("nothing should match nope.bin")
	}
	if b.IsBlacklisted("x R\\.log$") {
		t.Error("the unknown-type line must not become a filter")
	}

	joined := strings.Join(diagnostics, "\n")
	for _, want := range []string{
		"unknown flag 'z' in blacklist line 6",
		"unknown expression type in blacklist line 6",
		"empty expression in blacklist line 7",
		"invalid regexp in blacklist line 8",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("diagnostics missing %q:\n%s", want, joined)
		}
	}
}

// TestInitializeCRLFLines locks that CRLF-split lines keep their expressions
// intact: a trailing '\r' must not end up inside the regexp.
func TestInitializeCRLFLines(t *testing.T) {
	var b Blacklist
	b.Initialize([]string{"R \\.txt$\r"})
	if !b.IsBlacklisted("a.txt") {
		t.Error("a CRLF line lost its filter")
	}
}

// TestLoadBlacklistMissingFile locks the default state: no blacklist.txt means
// nothing is filtered and no error.
func TestLoadBlacklistMissingFile(t *testing.T) {
	b, err := LoadBlacklist(filepath.Join(t.TempDir(), "blacklist.txt"))
	if err != nil {
		t.Fatalf("LoadBlacklist: %v", err)
	}
	if got := b.Diagnostics(); len(got) != 0 {
		t.Errorf("diagnostics = %v, want none", got)
	}
	if b.IsBlacklisted("anything") {
		t.Error("an empty blacklist must filter nothing")
	}
}

// TestLoadBlacklistReadError locks the D51 boundary: a read failure other than
// a missing file is an error, never a silent empty blacklist. A directory in
// place of the file makes the read fail on every platform without the file
// being absent.
func TestLoadBlacklistReadError(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadBlacklist(dir); err == nil {
		t.Fatal("reading a directory must fail")
	}
}

// TestLoadBlacklistCarriesDiagnostics locks the round-2 audit fix: the approved
// two-value signature keeps the parse diagnostics reachable through the value
// itself, so a caller that loaded from a path can still render them.
func TestLoadBlacklistCarriesDiagnostics(t *testing.T) {
	path := filepath.Join(t.TempDir(), "blacklist.txt")
	if err := os.WriteFile(path, []byte("R ok\nR\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := LoadBlacklist(path)
	if err != nil {
		t.Fatalf("LoadBlacklist: %v", err)
	}
	if !b.IsBlacklisted("ok") {
		t.Error("the valid entry must keep filtering")
	}
	if got := b.Diagnostics(); len(got) != 1 ||
		!strings.Contains(got[0], "empty expression in blacklist line 2") {
		t.Errorf("diagnostics = %v, want the line-2 report", got)
	}
}
