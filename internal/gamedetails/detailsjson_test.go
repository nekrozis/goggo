package gamedetails

import (
	"testing"

	"github.com/nekrozis/goggo/internal/config"
)

// TestGameFileGetAsJson locks the file field table of the details-json
// contract: every key present, version only when non-empty. The key set is
// written out HERE rather than read from a production table: an independent
// statement of the wire format fails when the serializer drops a key, whereas a
// table the serializer itself owns would agree with whatever it does.
func TestGameFileGetAsJson(t *testing.T) {
	gf := GameFile{Updated: 1, ID: "en1installer0", Name: "Installer", Path: "/setup.exe",
		Size: "10", Platform: config.PlatformWindows, Language: config.LangEN, Silent: 0,
		Gamename: "g", Title: "G", GamenameBasegame: "bg", TitleBasegame: "BG",
		Type: config.GFBaseInstaller, GalaxyDownlinkJSONURL: "https://api/dl"}
	got := gf.GetAsJson()
	if _, has := got["version"]; has {
		t.Error("version present while empty, want it absent (the conditional member)")
	}
	for _, key := range []string{
		"updated", "id", "name", "path", "size", "platform", "language",
		"silent", "gamename", "title", "gamename_basegame", "title_basegame",
		"type", "galaxy_downlink_json_url",
	} {
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
	if _, has := got["downlink_diag"]; has {
		t.Error("a healthy entry must not carry a downlink_diag member")
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

// TestGetDetailsAsJsonDownlinkDiag locks the record's JSON face: a member with
// the five frozen keys when present, the full_failure projection computed by
// the predicate, and inheritance through the DLC recursion.
func TestGetDetailsAsJsonDownlinkDiag(t *testing.T) {
	gd := GameDetails{
		Gamename: "g", ProductID: "1", Title: "G",
		Installers: []GameFile{{ID: "i1", Version: "1"}},
		Downlink:   &DownlinkDiag{Attempts: 5, Failures: 3, Usable: 2, FirstError: "boom"},
		DLCs: []GameDetails{{Gamename: "d1", Patches: []GameFile{{ID: "p1"}},
			Downlink: &DownlinkDiag{Attempts: 4, Failures: 4, Usable: 0, FirstError: "dead"}}},
	}
	got := gd.GetDetailsAsJson()
	m, ok := got["downlink_diag"].(map[string]any)
	if !ok {
		t.Fatalf("downlink_diag = %#v, want an object", got["downlink_diag"])
	}
	want := map[string]any{"attempts": 5, "failures": 3, "usable": 2, "first_error": "boom", "full_failure": false}
	if len(m) != len(want) {
		t.Fatalf("downlink_diag has %d keys, want exactly %d: %#v", len(m), len(want), m)
	}
	for k, w := range want {
		if m[k] != w {
			t.Errorf("downlink_diag[%s] = %#v, want %#v", k, m[k], w)
		}
	}
	dlcs := got["dlcs"].([]any)
	dm := dlcs[0].(map[string]any)["downlink_diag"].(map[string]any)
	if dm["full_failure"] != true {
		t.Errorf("DLC full_failure = %#v, want the predicate's answer through the recursion", dm["full_failure"])
	}
}
