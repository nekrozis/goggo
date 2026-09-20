package model

import (
	"encoding/json"
	"testing"

	"github.com/nekrozis/goggo/internal/config"
)

func TestNewGameFileDefaults(t *testing.T) {
	f := NewGameFile()
	if f.Platform != config.PlatformWindows {
		t.Errorf("Platform = %d, want the Windows platform %d", f.Platform, config.PlatformWindows)
	}
	if f.Language != config.LangEN {
		t.Errorf("Language = %d, want the English language %d", f.Language, config.LangEN)
	}
	if f.Silent != 0 || f.Type != 0 || f.Updated != 0 {
		t.Errorf("Silent/Type/Updated defaults wrong: %d %d %d", f.Silent, f.Type, f.Updated)
	}
	if f.Version != "" {
		t.Errorf("Version = %q, want empty", f.Version)
	}
}

func TestGameFileMarshalJSONKeysAndTypes(t *testing.T) {
	f := NewGameFile()
	f.Updated = 7
	f.ID = "file-1"
	f.Name = "setup.exe"
	f.Path = "game/setup.exe"
	f.Size = "1048576"
	f.GalaxyDownlinkJSONURL = "https://example.invalid/downlink"
	f.Title = "Game"
	f.Score = 42          // must NOT appear in JSON
	f.Filepath = "/tmp/x" // must NOT appear in JSON

	raw, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	for _, k := range GameFileJSONKeys {
		if _, ok := obj[k]; !ok {
			t.Errorf("JSON missing key %q in %s", k, raw)
		}
	}
	for _, k := range []string{"version", "score", "filepath"} {
		if _, ok := obj[k]; ok {
			t.Errorf("JSON must not contain %q: %s", k, raw)
		}
	}
	// Value types: numeric fields stay numeric, size stays a string.
	if v, ok := obj["updated"].(float64); !ok || v != 7 {
		t.Errorf("updated type/value wrong: %#v", obj["updated"])
	}
	if v, ok := obj["size"].(string); !ok || v != "1048576" {
		t.Errorf("size must be a string, got %#v", obj["size"])
	}
	if v, ok := obj["platform"].(float64); !ok || v != 1 {
		t.Errorf("platform wrong: %#v", obj["platform"])
	}
	// The explicit key list above and the must-not-contain list are the
	// contract; a count would break on any legitimate new field.
}

func TestGameFileMarshalJSONVersionOnlyWhenSet(t *testing.T) {
	f := NewGameFile()
	raw, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var obj map[string]any
	_ = json.Unmarshal(raw, &obj)
	if _, ok := obj["version"]; ok {
		t.Errorf("version must be omitted when empty: %s", raw)
	}

	f.Version = "1.2.3"
	raw, err = json.Marshal(f)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if v, ok := obj["version"]; !ok || v != "1.2.3" {
		t.Errorf("version wrong when set: %s", raw)
	}
}
