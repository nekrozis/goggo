package gamedetails

import (
	"context"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"errors"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/nekrozis/goggo/internal/config"
)

// fullMask is every file type this conversion knows about.
func fullMask() uint32 {
	return config.GFBaseInstaller | config.GFBaseExtra | config.GFBasePatch |
		config.GFBaseLangPack | config.GFDLC
}

// testConfig is the download configuration the conversion reads: everything
// included, every platform and language accepted.
func testConfig() config.DownloadConfig {
	return config.DownloadConfig{
		Include:           fullMask(),
		InstallerPlatform: config.PlatformWindows | config.PlatformLinux | config.PlatformMac,
		InstallerLanguage: config.LangEN | config.LangFR,
	}
}

// stubResolver derives a path by a rule the tests can predict. The real
// derivation (galaxy.PathFromDownlinkURL) is deliberately not imported here: it
// belongs to the integration layer.
func stubResolver(failFor map[string]error) DownlinkResolver {
	return func(_ context.Context, gamename, downlinkURL string) (ResolvedFile, error) {
		if err, ok := failFor[downlinkURL]; ok {
			return ResolvedFile{}, err
		}
		return ResolvedFile{
			URL:  "https://cdn.example/" + gamename + "/" + downlinkURL,
			Path: "/" + gamename + "/" + downlinkURL,
		}, nil
	}
}

// fileEntry is one file inside an info node.
func fileEntry(id, downlink, size string) map[string]any {
	return map[string]any{"id": id, "downlink": downlink, "size": size}
}

// infoNode is one entry of a downloads vector.
func infoNode(name, osName, language string, files ...map[string]any) map[string]any {
	list := make([]any, 0, len(files))
	for _, f := range files {
		list = append(list, f)
	}
	return map[string]any{
		"name":       name,
		"version":    "1.0",
		"os":         osName,
		"language":   language,
		"count":      len(files),
		"total_size": 100,
		"files":      list,
	}
}

func nodeList(nodes ...map[string]any) []any {
	list := make([]any, 0, len(nodes))
	for _, n := range nodes {
		list = append(list, n)
	}
	return list
}

// product builds a minimal product document bytes. Pass a nil value to omit a field.
func product(fields map[string]any) []byte {
	base := map[string]any{
		"slug":  "the_game",
		"id":    "42",
		"title": "The Game",
	}
	for k, v := range fields {
		if v == nil {
			delete(base, k)
			continue
		}
		base[k] = v
	}
	b, err := jsonv2.Marshal(base, jsonv2.Deterministic(true))
	if err != nil {
		panic(err)
	}
	return b
}

// TestProductInfoToGameDetailsMapsStringsAndImages locks the metadata mapping:
// slug/id/title, the unconditional "https:" prefix on both images, the logo
// rename and the optional changelog.
func TestProductInfoToGameDetailsMapsStringsAndImages(t *testing.T) {
	doc := product(map[string]any{
		"images": map[string]any{
			"icon": "//images.gog.com/game_icon.jpg",
			"logo": "//images.gog.com/game_glx_logo.jpg",
		},
		"changelog": "1.0.1 — fixes",
	})

	gd, err := ProductInfoToGameDetails(context.Background(), doc, testConfig(), nil, stubResolver(nil))
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if gd.Gamename != "the_game" || gd.ProductID != "42" || gd.Title != "The Game" {
		t.Errorf("identity = %q/%q/%q", gd.Gamename, gd.ProductID, gd.Title)
	}
	if gd.Icon != "https://images.gog.com/game_icon.jpg" {
		t.Errorf("icon = %q", gd.Icon)
	}
	if gd.Logo != "https://images.gog.com/game.jpg" {
		t.Errorf("logo = %q, want the _glx_logo variant renamed", gd.Logo)
	}
	if gd.Changelog != "1.0.1 — fixes" {
		t.Errorf("changelog = %q", gd.Changelog)
	}

	// A product without the optional pieces converts to empty strings.
	bare, err := ProductInfoToGameDetails(context.Background(), product(nil), testConfig(), nil, stubResolver(nil))
	if err != nil {
		t.Fatalf("convert bare: %v", err)
	}
	if bare.Icon != httpsPrefix || bare.Logo != httpsPrefix || bare.Changelog != "" {
		t.Errorf("bare = icon %q, logo %q, changelog %q", bare.Icon, bare.Logo, bare.Changelog)
	}
	if len(bare.Installers) != 0 || len(bare.DLCs) != 0 {
		t.Errorf("a product with no downloads must produce no files: %+v", bare)
	}
}

// TestProductInfoToGameDetailsGatesVectorsByMask locks the mask gate: the gate is
// the composite bit (base|dlc), so a mask that only carries the DLC installer bit
// still converts the base installers vector.
func TestProductInfoToGameDetailsGatesVectorsByMask(t *testing.T) {
	doc := product(map[string]any{
		"downloads": map[string]any{
			"installers":     nodeList(infoNode("setup", "windows", "en", fileEntry("1", "setup.exe", "10"))),
			"bonus_content":  nodeList(infoNode("art", "", "", fileEntry("2", "art.zip", "10"))),
			"patches":        nodeList(infoNode("patch", "windows", "en", fileEntry("3", "patch.exe", "10"))),
			"language_packs": nodeList(infoNode("lang", "windows", "fr", fileEntry("4", "fr.exe", "10"))),
		},
	})

	all, err := ProductInfoToGameDetails(context.Background(), doc, testConfig(), nil, stubResolver(nil))
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if len(all.Installers) != 1 || len(all.Extras) != 1 || len(all.Patches) != 1 || len(all.LanguagePacks) != 1 {
		t.Errorf("full mask = %d/%d/%d/%d, want one each",
			len(all.Installers), len(all.Extras), len(all.Patches), len(all.LanguagePacks))
	}

	cfg := testConfig()
	cfg.Include = config.GFDLCInstaller // the DLC installer bit only
	dlcOnly, err := ProductInfoToGameDetails(context.Background(), doc, cfg, nil, stubResolver(nil))
	if err != nil {
		t.Fatalf("convert dlc-only: %v", err)
	}
	if len(dlcOnly.Installers) != 1 {
		t.Errorf("installers = %d, want the base vector converted too (the composite gate)",
			len(dlcOnly.Installers))
	}
	if len(dlcOnly.Extras)+len(dlcOnly.Patches)+len(dlcOnly.LanguagePacks) != 0 {
		t.Errorf("vectors of other types must stay empty: %+v", dlcOnly)
	}

	cfg.Include = 0
	none, err := ProductInfoToGameDetails(context.Background(), doc, cfg, nil, stubResolver(nil))
	if err != nil {
		t.Fatalf("convert none: %v", err)
	}
	if len(none.Installers)+len(none.Extras)+len(none.Patches)+len(none.LanguagePacks) != 0 {
		t.Errorf("an empty mask must convert no vectors: %+v", none)
	}
}

// TestProductInfoToGameDetailsFiltersByPlatformAndLanguage locks the entry
// filter and the extras exemption: installers are kept
// only when their os/language intersects the configuration, extras are exempt.
func TestProductInfoToGameDetailsFiltersByPlatformAndLanguage(t *testing.T) {
	doc := product(map[string]any{
		"downloads": map[string]any{
			"installers": nodeList(
				infoNode("win", "windows", "en", fileEntry("1", "win.exe", "10")),
				infoNode("lin", "linux", "en", fileEntry("2", "lin.sh", "10")),
				infoNode("fr", "windows", "fr", fileEntry("3", "fr.exe", "10")),
			),
			"bonus_content": nodeList(
				infoNode("art1", "", "", fileEntry("4", "art1.zip", "10")),
				infoNode("art2", "linux", "", fileEntry("5", "art2.zip", "10")),
			),
		},
	})

	cfg := testConfig()
	cfg.InstallerPlatform = config.PlatformWindows
	cfg.InstallerLanguage = config.LangEN
	gd, err := ProductInfoToGameDetails(context.Background(), doc, cfg, nil, stubResolver(nil))
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if len(gd.Installers) != 1 || gd.Installers[0].ID != "1" {
		t.Errorf("installers = %+v, want only the Windows/English one", gd.Installers)
	}
	if len(gd.Extras) != 2 {
		t.Errorf("extras = %+v, want both entries: extras are not filtered", gd.Extras)
	}
	for _, gf := range gd.Extras {
		// The Windows/English defaults are only used by the filter extras skip,
		// so neither the filter nor the assignment runs and the entry keeps the
		// zero platform and language.
		if gf.Platform != 0 || gf.Language != 0 {
			t.Errorf("extras entry = %+v, want platform/language left at zero", gf)
		}
	}
}

// TestProductInfoToGameDetailsSkipsEmptyEntries locks the issue-200 skip: an
// entry advertising neither count nor total_size is dropped entirely.
func TestProductInfoToGameDetailsSkipsEmptyEntries(t *testing.T) {
	empty := map[string]any{
		"name": "ghost", "version": "", "os": "windows", "language": "en",
		"count": 0, "total_size": 0,
		"files": []any{fileEntry("9", "ghost.exe", "10")},
	}
	doc := product(map[string]any{
		"downloads": map[string]any{
			"installers": nodeList(
				empty,
				infoNode("real", "windows", "en", fileEntry("1", "real.exe", "10")),
			),
		},
	})

	gd, err := ProductInfoToGameDetails(context.Background(), doc, testConfig(), nil, stubResolver(nil))
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if len(gd.Installers) != 1 || gd.Installers[0].ID != "1" {
		t.Errorf("installers = %+v, want the empty entry skipped", gd.Installers)
	}
}

// TestProductInfoToGameDetailsSkipsUnusableFiles locks the two per-file skips:
// a resolver that fails, and a resolved path that does not point at a file.
func TestProductInfoToGameDetailsSkipsUnusableFiles(t *testing.T) {
	doc := product(map[string]any{
		"downloads": map[string]any{
			"installers": nodeList(infoNode("setup", "windows", "en",
				fileEntry("1", "unresolvable.exe", "10"),
				fileEntry("2", "secure", "10"),
				fileEntry("3", "securex", "10"),
				fileEntry("4", "SECURE", "10"),
				fileEntry("5", "good.exe", "10"),
			)),
		},
	})

	// The resolver answers with a path built from the downlink name, so this
	// fixture's downlink names are the paths the "/securex?$" rule sees.
	resolve := func(_ context.Context, gamename, downlinkURL string) (ResolvedFile, error) {
		if downlinkURL == "unresolvable.exe" {
			return ResolvedFile{}, errors.New("empty downlink document")
		}
		return ResolvedFile{URL: "https://cdn.example/" + downlinkURL, Path: "/" + gamename + "/" + downlinkURL}, nil
	}
	gd, err := ProductInfoToGameDetails(context.Background(), doc, testConfig(), nil, resolve)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if len(gd.Installers) != 1 || gd.Installers[0].ID != "5" {
		t.Errorf("installers = %+v, want only the good file", gd.Installers)
	}
}

// TestProductInfoToGameDetailsKeepsTheResolvedPath locks the seam contract:
// Path is what the resolver reported, GalaxyDownlinkJSONURL is the original
// downlink, and no resolved-URL field exists on GameFile.
func TestProductInfoToGameDetailsKeepsTheResolvedPath(t *testing.T) {
	doc := product(map[string]any{
		"downloads": map[string]any{
			"installers": nodeList(infoNode("setup", "windows", "en", fileEntry("1", "setup.exe", "10"))),
		},
	})
	const original = "setup.exe"

	gd, err := ProductInfoToGameDetails(context.Background(), doc, testConfig(), nil, stubResolver(nil))
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if len(gd.Installers) != 1 {
		t.Fatalf("installers = %+v", gd.Installers)
	}
	gf := gd.Installers[0]
	if gf.Path != "/the_game/"+original {
		t.Errorf("path = %q, want what the resolver reported", gf.Path)
	}
	if gf.GalaxyDownlinkJSONURL != original {
		t.Errorf("galaxy downlink = %q, want the original downlink", gf.GalaxyDownlinkJSONURL)
	}
	if gf.Size != "10" || gf.Version != "1.0" || gf.Name != "setup" || gf.Title != "The Game" {
		t.Errorf("file = %+v", gf)
	}
	// The resolved URL is an intermediate value and must have no field of its own.
	if _, ok := reflect.TypeOf(gf).FieldByName("URL"); ok {
		t.Error("GameFile grew a resolved-URL field: the resolved URL is intermediate only")
	}
	if gf.Updated != 0 {
		t.Errorf("updated = %d, want the 'assume not updated' 0", gf.Updated)
	}
}

// TestProductInfoToGameDetailsDuplicateHandler locks the two behaviours of the
// duplicate handler: one row per path, and a language
// union when the same path is offered for several languages.
func TestProductInfoToGameDetailsDuplicateHandler(t *testing.T) {
	doc := product(map[string]any{
		"downloads": map[string]any{
			"installers": nodeList(
				infoNode("en", "windows", "en", fileEntry("1", "setup.exe", "10")),
				infoNode("fr", "windows", "fr", fileEntry("2", "setup.exe", "10")),
			),
		},
	})

	cfg := testConfig()
	cfg.DuplicateHandler = true
	merged, err := ProductInfoToGameDetails(context.Background(), doc, cfg, nil, stubResolver(nil))
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if len(merged.Installers) != 1 {
		t.Fatalf("installers = %+v, want one row for the shared path", merged.Installers)
	}
	if merged.Installers[0].Language != config.LangEN|config.LangFR {
		t.Errorf("language = %d, want both bits merged", merged.Installers[0].Language)
	}

	cfg.DuplicateHandler = false
	kept, err := ProductInfoToGameDetails(context.Background(), doc, cfg, nil, stubResolver(nil))
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if len(kept.Installers) != 2 {
		t.Errorf("installers = %+v, want both rows without the handler", kept.Installers)
	}
}

// TestIdentifierFieldsLiveAPIShapes locks the identifier reading against the
// shapes the live API sends: the identifier fields are not strings. These
// fixtures are parsed from JSON text (not hand-built maps) so the numbers decode
// the way the wire decodes them — a float64 — and the documents carry the shapes
// the probe captured from api.gog.com: a main document with a numeric id, a DLC
// with a numeric id, and the installer / bonus-content vectors whose file ids are
// a string and a number side by side.
func TestIdentifierFieldsLiveAPIShapes(t *testing.T) {
	doc := mustDocumentJSON(t, `{
		"id": 1207658991,
		"slug": "worms_united",
		"title": "Worms United",
		"downloads": {
			"installers": [{"name":"Worms United","version":"1.0","os":"windows","language":"en",
				"total_size":167772160,"files":[
					{"id":"en1installer0","downlink":"https://api.gog.com/products/1207658991/dl/installer","size":167772160}]}],
			"bonus_content": [{"name":"manual","count":1,"total_size":1048576,"files":[
					{"id":13403,"downlink":"https://api.gog.com/products/1207658991/dl/manual","size":1048576}]}],
			"patches": [], "language_packs": []
		}
	}`)

	gd, err := ProductInfoToGameDetails(context.Background(), doc, testConfig(), nil, stubResolver(nil))
	if err != nil {
		t.Fatalf("a live-API-shaped document must convert: %v", err)
	}
	if gd.ProductID != "1207658991" {
		t.Errorf("ProductID = %q, want the numeric id stringified", gd.ProductID)
	}
	if len(gd.Installers) != 1 || gd.Installers[0].ID != "en1installer0" {
		t.Errorf("installers = %+v, want the string file id kept as-is", gd.Installers)
	}
	if len(gd.Extras) != 1 || gd.Extras[0].ID != "13403" {
		t.Errorf("extras = %+v, want the numeric file id stringified", gd.Extras)
	}

	// bool and missing: true renders as "true", and an absent id is the empty
	// string — neither is an error.
	for _, tc := range []struct {
		name string
		id   string // JSON literal for the id member ("" = omit it)
		want string
	}{
		{name: "bool id", id: `true`, want: "true"},
		{name: "null id", id: `null`, want: ""},
		{name: "absent id", id: "", want: ""},
		{name: "string id survives", id: `"42"`, want: "42"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"slug":"s","title":"t"`
			if tc.id != "" {
				body += `,"id":` + tc.id
			}
			gd, err := ProductInfoToGameDetails(context.Background(),
				mustDocumentJSON(t, body+`}`), testConfig(), nil, stubResolver(nil))
			if err != nil {
				t.Fatalf("convert: %v", err)
			}
			if gd.ProductID != tc.want {
				t.Errorf("ProductID = %q, want %q", gd.ProductID, tc.want)
			}
		})
	}

	// M4: 64-bit integer id > 2^53 (58812465975493914) must not lose precision.
	t.Run("64-bit integer id > 2^53", func(t *testing.T) {
		m4Doc := mustDocumentJSON(t, `{"slug":"s","title":"t","id":58812465975493914}`)
		m4GD, err := ProductInfoToGameDetails(context.Background(), m4Doc, testConfig(), nil, stubResolver(nil))
		if err != nil {
			t.Fatalf("convert: %v", err)
		}
		if m4GD.ProductID != "58812465975493914" {
			t.Errorf("ProductID = %q, want 58812465975493914", m4GD.ProductID)
		}
	})

	// The structured id is the one shape the identifier reader still refuses.
	_, err = ProductInfoToGameDetails(context.Background(),
		mustDocumentJSON(t, `{"slug":"s","title":"t","id":{}}`), testConfig(), nil, stubResolver(nil))
	if err == nil {
		t.Fatal("an object id must be an error")
	}
	if _, err := ProductInfoToGameDetails(context.Background(),
		mustDocumentJSON(t, `{"slug":"s","title":"t","id":[]}`), testConfig(), nil, stubResolver(nil)); err == nil {
		t.Fatal("an array id must be an error")
	}

	// The DLC subtree through the same shapes: a numeric dlc id is converted
	// for the ownership gate, and the owned set matches the stringified form.
	dlcDoc := mustDocumentJSON(t, `{
		"id": 1, "slug":"base","title":"Base",
		"downloads":{"installers":[],"bonus_content":[],"patches":[],"language_packs":[]},
		"expanded_dlcs": [{"id": 1523284508, "slug":"base_dlc","title":"DLC",
			"downloads":{"installers":[{"name":"d","os":"windows","language":"en","version":"1",
				"count":1,"total_size":1,"files":[{"id":99,"downlink":"x","size":1}]}],
				"bonus_content":[],"patches":[],"language_packs":[]}}]
	}`)
	owned, err := ProductInfoToGameDetails(context.Background(), dlcDoc, testConfig(),
		map[string]bool{"1": true, "1523284508": true}, stubResolver(nil))
	if err != nil {
		t.Fatalf("owned gate on a numeric dlc id: %v", err)
	}
	if len(owned.DLCs) != 1 || owned.DLCs[0].ProductID != "1523284508" {
		t.Errorf("dlcs = %+v, want the numeric-id DLC matched and stringified", owned.DLCs)
	}
	if len(owned.DLCs) == 1 && len(owned.DLCs[0].Installers) == 1 && owned.DLCs[0].Installers[0].ID != "99" {
		t.Errorf("dlc file id = %q, want 99", owned.DLCs[0].Installers[0].ID)
	}
	if dropped, err := ProductInfoToGameDetails(context.Background(), dlcDoc, testConfig(),
		map[string]bool{"1": true}, stubResolver(nil)); err != nil {
		t.Fatalf("unowned gate: %v", err)
	} else if len(dropped.DLCs) != 0 {
		t.Errorf("dlcs = %+v, want the unowned numeric-id DLC dropped", dropped.DLCs)
	}
}

// mustDocumentJSON validates that body is valid JSON text and returns it as []byte.
func mustDocumentJSON(t *testing.T, body string) []byte {
	t.Helper()
	var doc any
	if err := jsonv2.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("fixture JSON: %v", err)
	}
	return []byte(body)
}

// dlcNode is one expanded DLC entry.
func dlcNode(id, title string, downloads map[string]any) map[string]any {
	return map[string]any{
		"slug":      "dlc_" + id,
		"id":        id,
		"title":     title,
		"downloads": downloads,
	}
}

// TestProductInfoToGameDetailsDLCSubtree locks the DLC rules
// : the ownership filter, the received type
// retyping, the basegame back-fill and the drop-if-empty rule.
func TestProductInfoToGameDetailsDLCSubtree(t *testing.T) {
	dlcDownloads := func(name string) map[string]any {
		return map[string]any{
			"installers": nodeList(infoNode(name, "windows", "en", fileEntry(name+"-1", name+".exe", "10"))),
		}
	}
	doc := product(map[string]any{
		"downloads": map[string]any{
			"installers": nodeList(infoNode("base", "windows", "en", fileEntry("b", "base.exe", "10"))),
			"bonus_content": nodeList(infoNode("base-art", "", "",
				fileEntry("ba", "no-dlc.exe", "10"))),
		},
		"expanded_dlcs": []any{
			dlcNode("100", "Owned DLC", dlcDownloads("owned")),
			dlcNode("200", "Unowned DLC", dlcDownloads("unowned")),
			dlcNode("300", "Empty DLC", map[string]any{}),
		},
	})

	owned := map[string]bool{"100": true, "300": true}
	gd, err := ProductInfoToGameDetails(context.Background(), doc, testConfig(), owned, stubResolver(nil))
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if len(gd.DLCs) != 1 {
		t.Fatalf("dlcs = %+v, want only the owned non-empty one", gd.DLCs)
	}
	dlc := gd.DLCs[0]
	if dlc.Gamename != "dlc_100" || dlc.ProductID != "100" {
		t.Errorf("dlc identity = %q/%q", dlc.Gamename, dlc.ProductID)
	}
	if dlc.TitleBasegame != "The Game" || dlc.GamenameBasegame != "the_game" {
		t.Errorf("basegame fields = %q/%q", dlc.TitleBasegame, dlc.GamenameBasegame)
	}
	if len(dlc.Installers) != 1 {
		t.Fatalf("dlc installers = %+v", dlc.Installers)
	}
	if dlc.Installers[0].Type != config.GFDLCInstaller {
		t.Errorf("type = %d, want the DLC installer bit", dlc.Installers[0].Type)
	}
	if dlc.Installers[0].TitleBasegame != "The Game" || dlc.Installers[0].GamenameBasegame != "the_game" {
		t.Errorf("file basegame fields = %q/%q", dlc.Installers[0].TitleBasegame, dlc.Installers[0].GamenameBasegame)
	}

	// An empty owned set means no filtering at all.
	unfiltered, err := ProductInfoToGameDetails(context.Background(), doc, testConfig(), map[string]bool{}, stubResolver(nil))
	if err != nil {
		t.Fatalf("convert unfiltered: %v", err)
	}
	if len(unfiltered.DLCs) != 2 {
		t.Errorf("dlcs = %d, want both non-empty entries when nothing filters them", len(unfiltered.DLCs))
	}
}

// TestProductInfoToGameDetailsNestedDLCs exercises the recursion two DLC levels
// deep, where the second level must still be filtered, retyped and back-filled
// correctly.
func TestProductInfoToGameDetailsNestedDLCs(t *testing.T) {
	level2 := dlcNode("1000", "Level 2", map[string]any{
		"patches": nodeList(infoNode("p2", "windows", "en", fileEntry("p2-1", "p2.exe", "10"))),
	})
	level1 := dlcNode("500", "Level 1", map[string]any{
		"patches": nodeList(infoNode("p1", "windows", "en", fileEntry("p1-1", "p1.exe", "10"))),
	})
	level1["expanded_dlcs"] = []any{level2}

	doc := product(map[string]any{
		"downloads": map[string]any{
			"installers": nodeList(infoNode("base", "windows", "en", fileEntry("b", "base.exe", "10"))),
		},
		"expanded_dlcs": []any{level1},
	})

	gd, err := ProductInfoToGameDetails(context.Background(), doc, testConfig(), nil, stubResolver(nil))
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if len(gd.DLCs) != 1 {
		t.Fatalf("dlcs = %+v, want one level-1 DLC", gd.DLCs)
	}
	l1 := gd.DLCs[0]
	if len(l1.DLCs) != 1 {
		t.Fatalf("level-1 dlcs = %+v, want the nested level-2 DLC kept", l1.DLCs)
	}
	l2 := l1.DLCs[0]
	if l2.Gamename != "dlc_1000" || l2.TitleBasegame != "Level 1" || l2.GamenameBasegame != "dlc_500" {
		t.Errorf("level-2 basegame fields = %q/%q (title) /%q (gamename), want the level-1 identity",
			l2.Gamename, l2.TitleBasegame, l2.GamenameBasegame)
	}
	if len(l2.Patches) != 1 || l2.Patches[0].Type != config.GFDLCPatch {
		t.Fatalf("level-2 patches = %+v, want the DLC patch bit", l2.Patches)
	}
	if l2.Patches[0].TitleBasegame != "Level 1" || l2.Patches[0].GamenameBasegame != "dlc_500" {
		t.Errorf("level-2 file basegame fields = %q/%q",
			l2.Patches[0].TitleBasegame, l2.Patches[0].GamenameBasegame)
	}

	// The ownership filter reaches the second level too.
	owned := map[string]bool{"500": true}
	filtered, err := ProductInfoToGameDetails(context.Background(), doc, testConfig(), owned, stubResolver(nil))
	if err != nil {
		t.Fatalf("convert filtered: %v", err)
	}
	if len(filtered.DLCs) != 1 || len(filtered.DLCs[0].DLCs) != 0 {
		t.Errorf("owned filtering must also drop an unowned nested DLC: %+v", filtered.DLCs)
	}
}

// TestProductInfoToGameDetailsRejectsWrongFieldShapes locks the type rule and the
// transaction boundary: a field that is read and has the wrong JSON type is an
// error, and the caller gets the ZERO GameDetails — never a half-built tree it
// could mistake for a result.
func TestProductInfoToGameDetailsRejectsWrongFieldShapes(t *testing.T) {
	withDownloads := func(downloads any) []byte {
		return product(map[string]any{"downloads": downloads})
	}
	cases := []struct {
		name string
		doc  []byte
	}{
		{"slug is not a string", product(map[string]any{"slug": 42})},
		{"images is not an object", product(map[string]any{"images": "nope"})},
		{"downloads is an array", withDownloads([]any{})},
		{"installers is an object", withDownloads(map[string]any{"installers": map[string]any{}})},
		{"info node is a string", withDownloads(map[string]any{
			"installers": []any{"not a node"}})},
		{"count is a string", withDownloads(map[string]any{
			"installers": nodeList(map[string]any{
				"name": "setup", "os": "windows", "language": "en",
				"count": "many", "total_size": 10, "files": []any{},
			})})},
		{"files is not an array", withDownloads(map[string]any{
			"installers": nodeList(map[string]any{
				"name": "setup", "os": "windows", "language": "en",
				"count": 1, "total_size": 10, "files": "nope",
			})})},
		{"file entry is not an object", withDownloads(map[string]any{
			"installers": nodeList(map[string]any{
				"name": "setup", "os": "windows", "language": "en",
				"count": 1, "total_size": 10, "files": []any{"nope"},
			})})},
		{"expanded_dlcs is an object", product(map[string]any{"expanded_dlcs": map[string]any{}})},
		// The failure lands AFTER a vector converted successfully: the boundary
		// must still hand back the zero value, never the half-built tree the
		// conversion had assembled by then.
		{"failure after a successful vector", product(map[string]any{
			"downloads": map[string]any{
				"installers": nodeList(infoNode("setup", "windows", "en", fileEntry("1", "setup.exe", "10"))),
			},
			"expanded_dlcs": map[string]any{},
		})},
		// A structured dlc id is the one shape the identifier reader refuses. A
		// numeric id belongs to the identifier test instead: it is read through a
		// conversion, not gated here.
		{"dlc id is an object", product(map[string]any{
			"expanded_dlcs": []any{map[string]any{"id": map[string]any{}}}})},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gd, err := ProductInfoToGameDetails(context.Background(), c.doc, testConfig(), nil, stubResolver(nil))
			if err == nil {
				t.Fatalf("convert succeeded, want an error for a field of the wrong shape: %+v", gd)
			}
			if !reflect.DeepEqual(gd, GameDetails{}) {
				t.Errorf("a failed conversion returned a partial tree: %+v", gd)
			}
			if !strings.HasPrefix(err.Error(), "gamedetails") {
				t.Errorf("error = %v, want it to name the conversion", err)
			}
		})
	}

	// The same document with the fields absent converts to zero values: absence is
	// not an error.
	bare, err := ProductInfoToGameDetails(context.Background(), product(nil), testConfig(), nil, stubResolver(nil))
	if err != nil {
		t.Fatalf("absent fields must not fail the conversion: %v", err)
	}
	if bare.Gamename != "the_game" {
		t.Errorf("gamename = %q", bare.Gamename)
	}
}

// TestPackageStaysOffTheNetwork is the purity guard: neither the production code
// nor the tests of this package may reach for the API client, the transport,
// the transfer layer or the command layers. The conversion is pure data, and
// the downlink seam is what keeps it that way.
func TestPackageStaysOffTheNetwork(t *testing.T) {
	forbidden := []string{
		"internal/galaxy",
		"internal/httpx",
		"internal/transfer",
		"internal/core",
		"internal/webapi",
		"internal/cli",
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), entry.Name(), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", entry.Name(), err)
		}
		for _, spec := range file.Imports {
			path := strings.Trim(spec.Path.Value, `"`)
			for _, bad := range forbidden {
				if path == bad || strings.HasPrefix(path, bad+"/") {
					t.Errorf("%s imports %s: this package must stay pure data", entry.Name(), path)
				}
			}
		}
	}
}

// TestProductInfoToGameDetailsAbsentOptionalFields locks the lenient half of the
// type rule on the fields a converter might be tempted to require.
func TestProductInfoToGameDetailsAbsentOptionalFields(t *testing.T) {
	doc := product(map[string]any{
		"downloads": map[string]any{
			"installers": nodeList(map[string]any{
				"name": "setup", "os": "windows", "language": "en",
				// no version, no count, no total_size, one file without size
				"files": []any{map[string]any{"id": "1", "downlink": "setup.exe"}},
			}),
		},
	})

	gd, err := ProductInfoToGameDetails(context.Background(), doc, testConfig(), nil, stubResolver(nil))
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	// count and total_size are both 0 here, so the entry is skipped by the
	// issue-200 rule rather than converted with empty strings.
	if len(gd.Installers) != 0 {
		t.Errorf("installers = %+v, want the entry skipped as empty", gd.Installers)
	}

	// With a size present the entry survives and the missing fields are empty.
	doc = product(map[string]any{
		"downloads": map[string]any{
			"installers": nodeList(map[string]any{
				"name": "setup", "os": "windows", "language": "en",
				"count": 1, "total_size": 10,
				"files": []any{map[string]any{"id": "1", "downlink": "setup.exe"}},
			}),
		},
	})
	gd, err = ProductInfoToGameDetails(context.Background(), doc, testConfig(), nil, stubResolver(nil))
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if len(gd.Installers) != 1 {
		t.Fatalf("installers = %+v", gd.Installers)
	}
	if gd.Installers[0].Version != "" || gd.Installers[0].Size != "" {
		t.Errorf("file = %+v, want empty version and size", gd.Installers[0])
	}
}

func TestReadFileSize(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantSz  uint64
		wantOk  bool
		wantErr bool
	}{
		{"string decimal", `"42"`, 42, true, false},
		{"number decimal", `42`, 42, true, false},
		{"zero number", `0`, 0, true, false},
		{"zero string", `"0"`, 0, true, false},
		{"empty string", `""`, 0, false, false},
		{"null", `null`, 0, false, false},
		{"non-numeric string", `"hello"`, 0, false, false},
		{"negative number", `-1`, 0, false, false},
		{"boolean", `true`, 0, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sz, ok, err := readFileSize(jsontext.Value(c.in))
			if (err != nil) != c.wantErr {
				t.Fatalf("readFileSize(%s) err = %v, wantErr = %v", c.in, err, c.wantErr)
			}
			if ok != c.wantOk {
				t.Errorf("readFileSize(%s) ok = %v, want %v", c.in, ok, c.wantOk)
			}
			if sz != c.wantSz {
				t.Errorf("readFileSize(%s) sz = %d, want %d", c.in, sz, c.wantSz)
			}
		})
	}
}

func TestProductInfoToGameDetailsUnknownFieldsPass(t *testing.T) {
	doc := []byte(`{
		"slug": "game",
		"id": "123",
		"title": "Game Title",
		"future_field": {"new_api": "x"},
		"unrecognized_array": [1, 2, 3]
	}`)
	gd, err := ProductInfoToGameDetails(context.Background(), doc, config.DownloadConfig{}, nil, stubResolver(nil))
	if err != nil {
		t.Fatalf("unexpected error on unknown fields: %v", err)
	}
	if gd.Gamename != "game" || gd.ProductID != "123" || gd.Title != "Game Title" {
		t.Fatalf("unexpected gd values: %+v", gd)
	}
}

// TestProductJSONBaseGameGolden locks the base game product.json artifact format.
//
// Contract (format): base game product.json preserves document member order,
// numeric literals beyond float64 precision, exponential and negative zero notation,
// escape spellings, and unmodelled fields with tab indentation and without a trailing newline.
func TestProductJSONBaseGameGolden(t *testing.T) {
	backslash := string([]byte{92})
	doc := "{\n" +
		"  \"z_custom\": \"extra\",\n" +
		"  \"slug\": \"game\",\n" +
		"  \"id\": 58812465975493914,\n" +
		"  \"title\": \"Game Title\",\n" +
		"  \"score\": 1e2,\n" +
		"  \"ratio\": -0,\n" +
		"  \"tag\": \"" + backslash + "u0041\",\n" +
		"  \"description\": \"unmodelled text\",\n" +
		"  \"downloads\": {}\n" +
		"}"

	cfg := testConfig()
	cfg.SaveProductJSON = true

	gd, err := ProductInfoToGameDetails(context.Background(), []byte(doc), cfg, nil, stubResolver(nil))
	if err != nil {
		t.Fatalf("ProductInfoToGameDetails: %v", err)
	}

	want := "{\n" +
		"\t\"z_custom\": \"extra\",\n" +
		"\t\"slug\": \"game\",\n" +
		"\t\"id\": 58812465975493914,\n" +
		"\t\"title\": \"Game Title\",\n" +
		"\t\"score\": 1e2,\n" +
		"\t\"ratio\": -0,\n" +
		"\t\"tag\": \"" + backslash + "u0041\",\n" +
		"\t\"description\": \"unmodelled text\",\n" +
		"\t\"downloads\": {}\n" +
		"}"

	if gd.ProductJson != want {
		t.Errorf("ProductJson =\n%s\nwant =\n%s", gd.ProductJson, want)
	}
}

// TestProductJSONDLCGolden locks the DLC product.json artifact format.
//
// Contract (format): DLC product.json formats the exact raw DLC document:
// it preserves unmodelled fields and does not synthesize wire fields (such as
// expanded_dlcs or empty download collections) that were absent in the source document.
func TestProductJSONDLCGolden(t *testing.T) {
	dlcBody := "{\n" +
		"  \"z_custom\": \"dlc_extra\",\n" +
		"  \"slug\": \"dlc_pack\",\n" +
		"  \"id\": 9007199254740993,\n" +
		"  \"title\": \"DLC Pack\",\n" +
		"  \"type\": \"DLC\",\n" +
		"  \"downloads\": {\n" +
		"    \"installers\": [\n" +
		"      {\n" +
		"        \"name\": \"Installer\",\n" +
		"        \"os\": \"windows\",\n" +
		"        \"language\": \"en\",\n" +
		"        \"total_size\": 100,\n" +
		"        \"files\": [\n" +
		"          {\"id\": \"f1\", \"downlink\": \"dl1\"}\n" +
		"        ]\n" +
		"      }\n" +
		"    ]\n" +
		"  }\n" +
		"}"

	doc := "{\n" +
		"  \"slug\": \"base_game\",\n" +
		"  \"id\": \"1\",\n" +
		"  \"title\": \"Base Game\",\n" +
		"  \"expanded_dlcs\": [\n" +
		dlcBody + "\n" +
		"  ]\n" +
		"}"

	cfg := testConfig()
	cfg.SaveProductJSON = true

	gd, err := ProductInfoToGameDetails(context.Background(), []byte(doc), cfg, nil, stubResolver(nil))
	if err != nil {
		t.Fatalf("ProductInfoToGameDetails: %v", err)
	}
	if len(gd.DLCs) != 1 {
		t.Fatalf("len(gd.DLCs) = %d, want 1", len(gd.DLCs))
	}

	want := "{\n" +
		"\t\"z_custom\": \"dlc_extra\",\n" +
		"\t\"slug\": \"dlc_pack\",\n" +
		"\t\"id\": 9007199254740993,\n" +
		"\t\"title\": \"DLC Pack\",\n" +
		"\t\"type\": \"DLC\",\n" +
		"\t\"downloads\": {\n" +
		"\t\t\"installers\": [\n" +
		"\t\t\t{\n" +
		"\t\t\t\t\"name\": \"Installer\",\n" +
		"\t\t\t\t\"os\": \"windows\",\n" +
		"\t\t\t\t\"language\": \"en\",\n" +
		"\t\t\t\t\"total_size\": 100,\n" +
		"\t\t\t\t\"files\": [\n" +
		"\t\t\t\t\t{\n" +
		"\t\t\t\t\t\t\"id\": \"f1\",\n" +
		"\t\t\t\t\t\t\"downlink\": \"dl1\"\n" +
		"\t\t\t\t\t}\n" +
		"\t\t\t\t]\n" +
		"\t\t\t}\n" +
		"\t\t]\n" +
		"\t}\n" +
		"}"

	if got := gd.DLCs[0].ProductJson; got != want {
		t.Errorf("DLC ProductJson =\n%s\nwant =\n%s", got, want)
	}
	// Diagnostic checks: ensure absent wire members were not synthesized.
	if strings.Contains(gd.DLCs[0].ProductJson, "expanded_dlcs") {
		t.Errorf("DLC ProductJson contains synthesized expanded_dlcs")
	}
	if strings.Contains(gd.DLCs[0].ProductJson, "bonus_content") {
		t.Errorf("DLC ProductJson contains synthesized bonus_content")
	}
}

// TestProductJSONPrecisionGuard locks verbatim integer preservation beyond float64 precision.
func TestProductJSONPrecisionGuard(t *testing.T) {
	doc := "{\n" +
		"  \"slug\": \"base_game\",\n" +
		"  \"id\": 58812465975493914,\n" +
		"  \"title\": \"Base Game\",\n" +
		"  \"downloads\": {},\n" +
		"  \"expanded_dlcs\": [\n" +
		"    {\n" +
		"      \"slug\": \"dlc_1\",\n" +
		"      \"id\": 9007199254740993,\n" +
		"      \"title\": \"DLC 1\",\n" +
		"      \"downloads\": {\"installers\": [{\"name\": \"i\", \"os\": \"windows\", \"language\": \"en\", \"total_size\": 100, \"files\": [{\"id\": \"1\", \"downlink\": \"d\"}]}]}\n" +
		"    }\n" +
		"  ]\n" +
		"}"

	cfg := testConfig()
	cfg.SaveProductJSON = true

	gd, err := ProductInfoToGameDetails(context.Background(), []byte(doc), cfg, nil, stubResolver(nil))
	if err != nil {
		t.Fatalf("ProductInfoToGameDetails: %v", err)
	}

	// 58812465975493914 > 2^53: float64 rounds it to 58812465975493910.
	if strings.Contains(gd.ProductJson, "58812465975493910") {
		t.Errorf("base ProductJson corrupted >2^53 integer to float64 rounded value")
	}
	if !strings.Contains(gd.ProductJson, "58812465975493914") {
		t.Errorf("base ProductJson missing verbatim integer 58812465975493914")
	}

	if len(gd.DLCs) != 1 {
		t.Fatalf("len(gd.DLCs) = %d, want 1", len(gd.DLCs))
	}
	// 9007199254740993 = 2^53 + 1: float64 rounds it to 9007199254740992.
	if strings.Contains(gd.DLCs[0].ProductJson, "9007199254740992") {
		t.Errorf("DLC ProductJson corrupted >2^53 integer to float64 rounded value")
	}
	if !strings.Contains(gd.DLCs[0].ProductJson, "9007199254740993") {
		t.Errorf("DLC ProductJson missing verbatim integer 9007199254740993")
	}
}

// TestProductJSONPreservationGuard locks unmodelled field retention.
func TestProductJSONPreservationGuard(t *testing.T) {
	doc := "{\n" +
		"  \"slug\": \"base_game\",\n" +
		"  \"id\": \"1\",\n" +
		"  \"title\": \"Base Game\",\n" +
		"  \"description\": \"rich html text\",\n" +
		"  \"extra_metadata\": {\"rating\": 5},\n" +
		"  \"downloads\": {},\n" +
		"  \"expanded_dlcs\": [\n" +
		"    {\n" +
		"      \"slug\": \"dlc_1\",\n" +
		"      \"id\": \"2\",\n" +
		"      \"title\": \"DLC 1\",\n" +
		"      \"unmodelled_feature\": true,\n" +
		"      \"downloads\": {\"installers\": [{\"name\": \"i\", \"os\": \"windows\", \"language\": \"en\", \"total_size\": 100, \"files\": [{\"id\": \"1\", \"downlink\": \"d\"}]}]}\n" +
		"    }\n" +
		"  ]\n" +
		"}"

	cfg := testConfig()
	cfg.SaveProductJSON = true

	gd, err := ProductInfoToGameDetails(context.Background(), []byte(doc), cfg, nil, stubResolver(nil))
	if err != nil {
		t.Fatalf("ProductInfoToGameDetails: %v", err)
	}

	if !strings.Contains(gd.ProductJson, "\"description\": \"rich html text\"") {
		t.Errorf("base ProductJson dropped unmodelled description field")
	}
	if !strings.Contains(gd.ProductJson, "\"extra_metadata\"") {
		t.Errorf("base ProductJson dropped unmodelled extra_metadata field")
	}

	if len(gd.DLCs) != 1 {
		t.Fatalf("len(gd.DLCs) = %d, want 1", len(gd.DLCs))
	}
	if !strings.Contains(gd.DLCs[0].ProductJson, "\"unmodelled_feature\": true") {
		t.Errorf("DLC ProductJson dropped unmodelled_feature field")
	}
}

// --- The downlink resolution record: any failure attaches, partial and full
// alike, and a dropped DLC keeps its evidence in the parent's counts ---

// TestDownlinkDiagPartialFailure: 5 files, 3 refused, 2 delivered. The record
// attaches on any failure, partial included, and is not a full failure.
func TestDownlinkDiagPartialFailure(t *testing.T) {
	doc := product(map[string]any{"downloads": map[string]any{
		"installers": nodeList(infoNode("setup", "windows", "en",
			fileEntry("1", "a.exe", "10"), fileEntry("2", "b.exe", "10"),
			fileEntry("3", "c.exe", "10"), fileEntry("4", "d.exe", "10"),
			fileEntry("5", "e.exe", "10"))),
	}})
	resolve := stubResolver(map[string]error{
		"a.exe": errors.New("GET a.exe: HTTP 404"),
		"b.exe": errors.New("GET b.exe: HTTP 404"),
		"c.exe": errors.New("GET c.exe: HTTP 404"),
	})
	gd, err := ProductInfoToGameDetails(context.Background(), doc, testConfig(), nil, resolve)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	d := gd.Downlink
	if d == nil {
		t.Fatal("partial failure must attach a record")
	}
	if d.Attempts != 5 || d.Failures != 3 || d.Usable != 2 {
		t.Errorf("record = %+v, want 5/3/2", d)
	}
	if d.FirstError != "GET a.exe: HTTP 404" {
		t.Errorf("first error = %q, want the first refusal", d.FirstError)
	}
	if d.FullFailure() {
		t.Error("2 usable files came back; this is not a full failure")
	}
	if len(gd.Installers) != 2 {
		t.Errorf("installers = %d, want the 2 successes kept (upstream per-file semantics)", len(gd.Installers))
	}
}

// TestDownlinkDiagTrueEmpty: a document with no file entries records nothing.
// The legitimate empty answer stays byte-identical to the healthy path.
func TestDownlinkDiagTrueEmpty(t *testing.T) {
	doc := product(map[string]any{"downloads": map[string]any{"installers": nodeList()}})
	gd, err := ProductInfoToGameDetails(context.Background(), doc, testConfig(), nil, stubResolver(nil))
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if gd.Downlink != nil {
		t.Errorf("record = %+v, want nil for a product with nothing to resolve", gd.Downlink)
	}
}

// TestDownlinkDiagAllUnusableIsNotFailure: the resolver answered every
// file, the /secure rule refused every path, no error occurred. An error-free
// unusable path is out of this record's scope by decision,
// not by omission.
func TestDownlinkDiagAllUnusableIsNotFailure(t *testing.T) {
	doc := product(map[string]any{"downloads": map[string]any{
		"installers": nodeList(infoNode("setup", "windows", "en",
			fileEntry("1", "secure", "10"), fileEntry("2", "securex", "10"))),
	}})
	gd, err := ProductInfoToGameDetails(context.Background(), doc, testConfig(), nil, stubResolver(nil))
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if gd.Downlink != nil {
		t.Errorf("record = %+v, want nil: unusable paths carry no failure evidence", gd.Downlink)
	}
	if len(gd.Installers) != 0 {
		t.Errorf("installers = %d, want the upstream skip preserved", len(gd.Installers))
	}
}

// TestDownlinkDiagMixedFullFailure: one error plus one unusable path
// leaves nothing usable with a recorded failure — full failure. A predicate
// of Failures==Attempts would miss exactly this.
func TestDownlinkDiagMixedFullFailure(t *testing.T) {
	doc := product(map[string]any{"downloads": map[string]any{
		"installers": nodeList(infoNode("setup", "windows", "en",
			fileEntry("1", "a.exe", "10"), fileEntry("2", "secure", "10"))),
	}})
	resolve := stubResolver(map[string]error{"a.exe": errors.New("GET a.exe: HTTP 404")})
	gd, err := ProductInfoToGameDetails(context.Background(), doc, testConfig(), nil, resolve)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	d := gd.Downlink
	if d == nil {
		t.Fatal("mixed error+unusable must attach a record")
	}
	if d.Attempts != 2 || d.Failures != 1 || d.Usable != 0 || !d.FullFailure() {
		t.Errorf("record = %+v, want {2,1,0} and full failure", d)
	}
}

// TestDownlinkDiagFilterCannotEraseEvidence: the statistics live before
// any filter, so a mask-shrunk empty vector must not masquerade as an API
// failure. A resolve error + a resolve success that the later filter drops
// still leaves Usable > 0.
func TestDownlinkDiagFilterCannotEraseEvidence(t *testing.T) {
	doc := product(map[string]any{"downloads": map[string]any{
		"installers": nodeList(infoNode("setup", "windows", "en",
			fileEntry("1", "a.exe", "10"), fileEntry("2", "ok.exe", "10"))),
	}})
	resolve := stubResolver(map[string]error{"a.exe": errors.New("GET a.exe: HTTP 404")})
	gd, err := ProductInfoToGameDetails(context.Background(), doc, testConfig(), nil, resolve)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	before := *gd.Downlink
	if before.Attempts != 2 || before.Failures != 1 || before.Usable != 1 || before.FullFailure() {
		t.Fatalf("record = %+v, want {2,1,1} not full", before)
	}
	gd.FilterWithType(config.GFExtra) // shrink to extras: every installer is dropped
	if len(gd.Installers) != 0 {
		t.Fatalf("installers survived the mask, fixture broken")
	}
	after := *gd.Downlink
	if after != before {
		t.Errorf("record changed to %+v after filtering; statistics must be filter-independent", after)
	}
	if after.FullFailure() {
		t.Error("a mask-shrunk empty vector must never read as a full API failure")
	}
}

// TestDownlinkDiagDiscardedDLCPreservesEvidence: a DLC whose every
// downlink fails is dropped from the display exactly as before, but its
// record survives in the parent's counts — and the parent's full-failure
// verdict stays correct in both directions.
func TestDownlinkDiagDiscardedDLCPreservesEvidence(t *testing.T) {
	dlcDownloads := map[string]any{
		"installers": nodeList(infoNode("d", "windows", "en",
			fileEntry("d1", "dlc-a.exe", "10"), fileEntry("d2", "dlc-b.exe", "10"))),
	}
	doc := product(map[string]any{
		"downloads": map[string]any{
			"installers": nodeList(infoNode("base", "windows", "en", fileEntry("b", "base.exe", "10"))),
		},
		"expanded_dlcs": []any{dlcNode("100", "Dead DLC", dlcDownloads)},
	})
	deadDLC := stubResolver(map[string]error{
		"dlc-a.exe": errors.New("GET dlc-a.exe: HTTP 404"),
		"dlc-b.exe": errors.New("GET dlc-b.exe: HTTP 404"),
	})

	gd, err := ProductInfoToGameDetails(context.Background(), doc, testConfig(), nil, deadDLC)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if len(gd.DLCs) != 0 {
		t.Fatalf("dlcs = %d, want the display drop unchanged", len(gd.DLCs))
	}
	d := gd.Downlink
	if d == nil {
		t.Fatal("the discarded DLC's failures must live on in the parent record")
	}
	if d.Attempts != 3 || d.Failures != 2 || d.Usable != 1 {
		t.Errorf("parent record = %+v, want {3,2,1} (base usable + dlc refused)", d)
	}
	if d.FullFailure() {
		t.Error("the base file resolved; this is not a full failure")
	}

	allDead := stubResolver(map[string]error{
		"base.exe":  errors.New("GET base.exe: HTTP 404"),
		"dlc-a.exe": errors.New("GET dlc-a.exe: HTTP 404"),
		"dlc-b.exe": errors.New("GET dlc-b.exe: HTTP 404"),
	})
	gd2, err := ProductInfoToGameDetails(context.Background(), doc, testConfig(), nil, allDead)
	if err != nil {
		t.Fatalf("convert all-dead: %v", err)
	}
	if gd2.Downlink == nil || !gd2.Downlink.FullFailure() {
		t.Errorf("parent record = %+v, want a full failure when base and dlc both refuse", gd2.Downlink)
	}
}
