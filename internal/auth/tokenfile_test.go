package auth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/nekrozis/goggo/internal/config"
)

func testGalaxy(t *testing.T, token map[string]any) *config.GalaxyConfig {
	t.Helper()
	g := config.NewGalaxyConfig()
	g.SetJSON(token)
	return g
}

func tokenMap(extra ...map[string]any) map[string]any {
	m := map[string]any{
		"access_token":  "at-1",
		"refresh_token": "rt-1",
		"expires_in":    3600,
		"user_id":       "u7",
	}
	for _, e := range extra {
		for k, v := range e {
			m[k] = v
		}
	}
	return m
}

func TestSaveLoadRoundTrip(t *testing.T) {
	g := testGalaxy(t, tokenMap())
	path := filepath.Join(t.TempDir(), "galaxy.json")
	if err := SaveTokenFile(g, path); err != nil {
		t.Fatalf("SaveTokenFile: %v", err)
	}

	g2 := config.NewGalaxyConfig()
	if err := LoadTokenFile(g2, path); err != nil {
		t.Fatalf("LoadTokenFile: %v", err)
	}
	// Values pass through JSON encoding on save and decoding on load, so
	// numeric types may change (int -> float64). Compare by JSON text.
	for k, want := range g.GetJSON() {
		wantJSON, err := json.Marshal(want)
		if err != nil {
			t.Fatal(err)
		}
		gotJSON, err := json.Marshal(g2.GetJSON()[k])
		if err != nil {
			t.Fatal(err)
		}
		if string(gotJSON) != string(wantJSON) {
			t.Errorf("field %q = %s, want %s", k, gotJSON, wantJSON)
		}
	}
}

// TestLoadMissingExpiresAtUsesMtime: with no expires_at in the file, it must be
// derived from the file mtime (NOT the current time) plus expires_in.
func TestLoadMissingExpiresAtUsesMtime(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "old.json")
	// A hand-written legacy file with no expires_at.
	legacy := map[string]any{
		"access_token":  "at-old",
		"refresh_token": "rt-old",
		"expires_in":    3600,
	}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	mt := time.Date(2020, 4, 15, 12, 30, 0, 0, time.Local)
	if err := os.Chtimes(path, mt, mt); err != nil {
		t.Fatal(err)
	}

	g := config.NewGalaxyConfig()
	if err := LoadTokenFile(g, path); err != nil {
		t.Fatalf("LoadTokenFile: %v", err)
	}
	want := mt.Unix() + 3600
	// LoadTokenFile injects expires_at as an int64 before SetJSON.
	got, ok := g.GetJSON()["expires_at"].(int64)
	if !ok {
		t.Fatalf("expires_at = %T %v, want int64 %d", g.GetJSON()["expires_at"], g.GetJSON()["expires_at"], want)
	}
	if got != want {
		t.Errorf("expires_at = %d, want %d (file mtime + expires_in)", got, want)
	}
}

// TestLoadMissingExpiresAtNoExpiresIn: with expires_in also missing, the value
// contributes 0, so expires_at lands exactly on the file mtime (already-expired
// token).
func TestLoadMissingExpiresAtNoExpiresIn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bare.json")
	data := []byte(`{"access_token":"at","refresh_token":"rt"}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	mt := time.Date(2020, 1, 1, 0, 0, 0, 0, time.Local)
	if err := os.Chtimes(path, mt, mt); err != nil {
		t.Fatal(err)
	}

	g := config.NewGalaxyConfig()
	if err := LoadTokenFile(g, path); err != nil {
		t.Fatalf("LoadTokenFile: %v", err)
	}
	if got := g.GetJSON()["expires_at"].(int64); got != mt.Unix() {
		t.Errorf("expires_at = %d, want mtime %d", got, mt.Unix())
	}
}

func TestSaveEmptyStoreWritesNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.json")
	g := config.NewGalaxyConfig() // empty store
	if err := SaveTokenFile(g, path); err != nil {
		t.Fatalf("SaveTokenFile: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file exists after empty save, stat err = %v", err)
	}
}

func TestLoadMissingFileError(t *testing.T) {
	g := config.NewGalaxyConfig()
	path := filepath.Join(t.TempDir(), "nope.json")
	if err := LoadTokenFile(g, path); err == nil {
		t.Fatal("LoadTokenFile: want error for missing file")
	}
}

func TestLoadBadJSONError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	g := config.NewGalaxyConfig()
	if err := LoadTokenFile(g, path); err == nil {
		t.Fatal("LoadTokenFile: want error for bad JSON")
	}
}

func TestLoadNonObjectJSONError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "array.json")
	if err := os.WriteFile(path, []byte(`["a","b"]`), 0o600); err != nil {
		t.Fatal(err)
	}
	g := config.NewGalaxyConfig()
	if err := LoadTokenFile(g, path); err == nil {
		t.Fatal("LoadTokenFile: want error for non-object JSON")
	}
}

// TestSavedFileModeIs0600 verifies the atomic write lands at 0600 and leaves
// no temp files behind. Unix-only: Windows permission semantics differ.
func TestSavedFileModeIs0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission mode assertions are Unix-only")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "galaxy.json")
	g := testGalaxy(t, tokenMap())
	if err := SaveTokenFile(g, path); err != nil {
		t.Fatalf("SaveTokenFile: %v", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Errorf("mode = %o, want 0600", got)
	}
	leftovers, err := filepath.Glob(filepath.Join(dir, ".goggo-token-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(leftovers) != 0 {
		t.Errorf("temp files left behind: %v", leftovers)
	}
}

func TestSaveOverwritesExisting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "galaxy.json")
	if err := SaveTokenFile(testGalaxy(t, tokenMap()), path); err != nil {
		t.Fatal(err)
	}
	replacement := tokenMap(map[string]any{"access_token": "at-2", "refresh_token": "rt-2"})
	if err := SaveTokenFile(testGalaxy(t, replacement), path); err != nil {
		t.Fatalf("second SaveTokenFile: %v", err)
	}
	g := config.NewGalaxyConfig()
	if err := LoadTokenFile(g, path); err != nil {
		t.Fatal(err)
	}
	if got := g.GetAccessToken(); got != "at-2" {
		t.Errorf("access token = %q, want at-2", got)
	}
}
