package gamedetails

import (
	"testing"

	"github.com/nekrozis/goggo/internal/config"
	"github.com/nekrozis/goggo/internal/model"
)

// TestGameFileGetAsJson locks the file field table of the details-json
// contract: every key present, version only when non-empty. The key set is
// shared with the model package's serializer test (model.GameFileJSONKeys) —
// two implementations of one wire format — while the assertions stay separate,
// because the two methods may render values differently.
func TestGameFileGetAsJson(t *testing.T) {
	gf := GameFile{Updated: 1, ID: "en1installer0", Name: "Installer", Path: "/setup.exe",
		Size: "10", Platform: config.PlatformWindows, Language: config.LangEN, Silent: 0,
		Gamename: "g", Title: "G", GamenameBasegame: "bg", TitleBasegame: "BG",
		Type: config.GFBaseInstaller, GalaxyDownlinkJSONURL: "https://api/dl"}
	got := gf.GetAsJson()
	if _, has := got["version"]; has {
		t.Error("version present while empty, want it absent (the conditional member)")
	}
	for _, key := range model.GameFileJSONKeys {
		if _, has := got[key]; !has {
			t.Errorf("field %s missing from the file json", key)
		}
	}
	gf.Version = "1.2"
	if v, has := gf.GetAsJson()["version"]; !has || v != "1.2" {
		t.Error("version with value must appear")
	}
}

// TestGetDetailsAsJson locks the display contract: the header fields, the
// extras-first vector order (NOT the file-vector order), empty vectors absent,
// and the DLC recursion (gameDetails::getDetailsAsJson).
func TestGetDetailsAsJson(t *testing.T) {
	gd := GameDetails{
		Gamename: "g", ProductID: "1", Title: "G", Serials: "S", Changelog: "C", Icon: "i",
		Extras:     []GameFile{{ID: "e1"}},
		Installers: []GameFile{{ID: "i1", Version: "1"}},
		DLCs:       []GameDetails{{Gamename: "d1", Patches: []GameFile{{ID: "p1"}}}},
	}
	got := gd.GetDetailsAsJson()
	for _, key := range []string{"gamename", "gamename_basegame", "product_id", "title",
		"title_basegame", "icon", "serials", "changelog", "extras", "installers", "dlcs"} {
		if _, has := got[key]; !has {
			t.Errorf("key %s missing", key)
		}
	}
	if _, has := got["patches"]; has {
		t.Error("empty vectors must be absent members, not empty arrays")
	}
	if _, has := got["languagepacks"]; has {
		t.Error("empty languagepacks must be absent")
	}
	dlcs, ok := got["dlcs"].([]any)
	if !ok || len(dlcs) == 0 {
		t.Fatalf("dlcs = %#v, want a non-empty array", got["dlcs"])
	}
	dlc, ok := dlcs[0].(map[string]any)
	if !ok {
		t.Fatalf("dlcs[0] = %#v, want an object", dlcs[0])
	}
	if _, has := dlc["extras"]; has {
		t.Error("the DLC's empty vectors must be absent too")
	}
	if dlc["gamename"] != "d1" {
		t.Errorf("dlc gamename = %v", dlc["gamename"])
	}
}
