package gamedetails

import (
	"context"
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

// stubResolver is the seam as GD1 sees it: it derives a path by a rule the tests
// can predict. The real derivation (galaxy.PathFromDownlinkURL) is deliberately
// NOT imported here — it is GD2's integration concern (review GD1 §4.2).
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

// product builds a minimal product document. Pass a nil value to omit a field.
func product(fields map[string]any) map[string]any {
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
	return base
}

// TestProductInfoToGameDetailsMapsStringsAndImages locks the metadata mapping:
// slug/id/title, the unconditional "https:" prefix on both images, the logo
// rename and the optional changelog (galaxyapi.cpp:395-402).
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

// TestProductInfoToGameDetailsGatesVectorsByMask locks the mask gate, including
// the upstream composite behaviour: the gate is the composite bit
// (base|dlc), so a mask that only carries the DLC installer bit still converts
// the base installers vector (galaxyapi.cpp:404-410). That is upstream's own
// behaviour and is kept deliberately.
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
		t.Errorf("installers = %d, want the base vector converted too (composite gate, upstream behaviour)",
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
// filter and the extras exemption (galaxyapi.cpp:516-537): installers are kept
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
		// Upstream's Windows/English defaults live in local variables that are
		// only used for the filter it skips for extras: neither the filter nor
		// the assignment runs, so the entry keeps the zero platform/language
		// (galaxyapi.cpp:516-537, 566-570).
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
// a resolver that fails, and a path upstream rejects with "/securex?$"
// (galaxyapi.cpp:530-557).
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
// downlink, and no resolved-URL field exists on GameFile (ruling A1).
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
		t.Errorf("updated = %d, want the upstream 'assume not updated' 0", gf.Updated)
	}
}

// TestProductInfoToGameDetailsDuplicateHandler locks the two behaviours of the
// duplicate handler (galaxyapi.cpp:571-586): one row per path, and a language
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
// (galaxyapi.cpp:436-495): the ownership filter, the received type
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

	// An empty owned set means no filtering at all (upstream tests emptiness).
	unfiltered, err := ProductInfoToGameDetails(context.Background(), doc, testConfig(), map[string]bool{}, stubResolver(nil))
	if err != nil {
		t.Fatalf("convert unfiltered: %v", err)
	}
	if len(unfiltered.DLCs) != 2 {
		t.Errorf("dlcs = %d, want both non-empty entries when nothing filters them", len(unfiltered.DLCs))
	}
}

// TestProductInfoToGameDetailsNestedDLCs exercises the recursion the way the
// S-GD1 review asked (Δ-GD1-R1): two DLC levels deep, where the second level
// must still be filtered, retyped and back-filled correctly.
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

// TestProductInfoToGameDetailsRejectsWrongFieldShapes locks the type rule and
// the transaction boundary the review asked for: a field that is read and has
// the wrong JSON type is an error, and the caller gets the ZERO GameDetails —
// never a half-built tree it could mistake for a result.
func TestProductInfoToGameDetailsRejectsWrongFieldShapes(t *testing.T) {
	withDownloads := func(downloads any) map[string]any {
		return product(map[string]any{"downloads": downloads})
	}
	cases := []struct {
		name string
		doc  map[string]any
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
		{"dlc id is missing a shape", product(map[string]any{
			"expanded_dlcs": []any{map[string]any{"id": 7}}})},
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

	// The same document with the fields absent converts to zero values: absence
	// is the upstream lenient reading, not an error.
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
// the downlink seam is what keeps it that way (review GD1 §4.3).
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
