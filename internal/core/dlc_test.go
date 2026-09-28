package core

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/nekrozis/goggo/internal/config"
	"github.com/nekrozis/goggo/internal/galaxy"
)

// The two DLC products the selection fixtures carry, with the titles their
// product documents answer.
const (
	dlcATestID = "900001"
	dlcBTestID = "900002"
)

const (
	dlcATestTitle = "Legacy of Rome"
	dlcBTestTitle = "Sunset Invasion"
)

// newDLCFixture builds a plan fixture whose build carries a base depot and two
// DLC products, each with its own file and its own title, so a selection is
// observable both in the plan and in the listing.
func newDLCFixture(t *testing.T) (*planFixture, config.Config) {
	t.Helper()
	f := newPlanFixture(t)
	f.setDefaultBodies()

	v2New := galaxy.HashToGalaxyPath(planBuildHashNew)
	baseHash := "9a00000000000000000000000000000000000001"
	aHash := "9a00000000000000000000000000000000000002"
	bHash := "9a00000000000000000000000000000000000003"
	f.set("/content-system/v2/meta/"+v2New,
		`{"baseProductId":"`+planProductID+`","installDirectory":"W3 GOTY","version":2,`+
			`"products":[{"name":"The Witcher 3: Wild Hunt"}],`+
			`"depots":[`+
			`{"productId":"`+planProductID+`","languages":["en-US"],"osBitness":["64"],"manifest":"`+baseHash+`"},`+
			`{"productId":"`+dlcATestID+`","languages":["en-US"],"osBitness":["64"],"manifest":"`+aHash+`"},`+
			`{"productId":"`+dlcBTestID+`","languages":["en-US"],"osBitness":["64"],"manifest":"`+bHash+`"}]}`)
	f.set("/content-system/v2/meta/"+galaxy.HashToGalaxyPath(baseHash),
		`{"depot":{"items":[{"path":"game/base.bin","chunks":[{"compressedMd5":"c0","md5":"u0","compressedSize":10,"size":10}]}]}}`)
	f.set("/content-system/v2/meta/"+galaxy.HashToGalaxyPath(aHash),
		`{"depot":{"items":[{"path":"game/dlc_a.bin","chunks":[{"compressedMd5":"c1","md5":"u1","compressedSize":10,"size":10}]}]}}`)
	f.set("/content-system/v2/meta/"+galaxy.HashToGalaxyPath(bHash),
		`{"depot":{"items":[{"path":"game/dlc_b.bin","chunks":[{"compressedMd5":"c2","md5":"u2","compressedSize":10,"size":10}]}]}}`)
	f.set("/products/"+dlcATestID, `{"id":"`+dlcATestID+`","title":"`+dlcATestTitle+`"}`)
	f.set("/products/"+dlcBTestID, `{"id":"`+dlcBTestID+`","title":"`+dlcBTestTitle+`"}`)

	return f, planTestConfig(t)
}

// plannedPaths is the set of paths the plan will transfer.
func plannedPaths(res PlanResult) map[string]bool {
	paths := make(map[string]bool, len(res.Plan.Tasks))
	for _, task := range res.Plan.Tasks {
		paths[task.Item.Path] = true
	}
	return paths
}

// TestDLCProducts locks the discovery: every depot product other than the base
// product, once each, in a stable order.
func TestDLCProducts(t *testing.T) {
	manifest := map[string]any{
		"depots": []any{
			map[string]any{"productId": "base"},
			map[string]any{"productId": "222"},
			map[string]any{"productId": "111"},
			map[string]any{"productId": "222"},
			map[string]any{"productId": ""},
			map[string]any{},
		},
	}
	got, err := dlcProducts(manifest, "base")
	if err != nil {
		t.Fatalf("dlcProducts: %v", err)
	}
	if len(got) != 2 || got[0] != "111" || got[1] != "222" {
		t.Errorf("products = %v, want [111 222]", got)
	}
}

// TestResolveDLCSelector locks the matching rule: an exact product id, or the
// full title normalized. Nothing else — a substring would make ordinary words
// ambiguous, and two DLCs sharing a title are ambiguous by definition.
func TestResolveDLCSelector(t *testing.T) {
	products := []string{"111", "222", "333"}
	ids := map[string]bool{"111": true, "222": true, "333": true}
	titles := map[string]string{
		"111": "Legacy of Rome",
		"222": "Sunset Invasion",
		"333": "Legacy of Rome",
	}

	cases := []struct {
		name    string
		value   string
		wantID  string
		wantWhy string
	}{
		{"exact product id", "111", "111", ""},
		{"exact title", "Sunset Invasion", "222", ""},
		{"title case and surrounding space are ignored", "  sunset INVASION  ", "222", ""},
		{"unknown value", "klingon", "", "is not a DLC of this build"},
		{"an id no DLC carries", "999", "", "is not a DLC of this build"},
		{"a substring is not a match", "Legacy", "", "is not a DLC of this build"},
		{"a shared title is ambiguous", "Legacy of Rome", "", "is ambiguous"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			id, problem := resolveDLCSelector(c.value, products, ids, titles)
			if c.wantWhy == "" {
				if problem != "" {
					t.Fatalf("problem = %q, want none", problem)
				}
				if id != c.wantID {
					t.Errorf("id = %q, want %q", id, c.wantID)
				}
				return
			}
			if !strings.Contains(problem, c.wantWhy) {
				t.Errorf("problem = %q, want it to mention %q", problem, c.wantWhy)
			}
		})
	}

	// The ambiguity has to be resolvable from the message alone.
	_, problem := resolveDLCSelector("Legacy of Rome", products, ids, titles)
	for _, want := range []string{"111", "333", "Legacy of Rome"} {
		if !strings.Contains(problem, want) {
			t.Errorf("ambiguous problem = %q, want it to mention %q", problem, want)
		}
	}
}

// TestBuildPlanInstallsEveryOwnedDLCByDefault locks the unchanged default: with
// no selector, every DLC the account owns is installed.
func TestBuildPlanInstallsEveryOwnedDLCByDefault(t *testing.T) {
	f, cfg := newDLCFixture(t)
	d := newOfflineDownloader(t, f.Server, cfg, newFakeConsole())

	res, err := d.BuildPlan(context.Background(), NewInstallRequest(cfg, planProductID, "", ProductRefExact))
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	for _, want := range []string{"game/base.bin", "game/dlc_a.bin", "game/dlc_b.bin"} {
		if !plannedPaths(res)[want] {
			t.Errorf("plan is missing %s: the default installs every owned DLC", want)
		}
	}
}

// TestBuildPlanSelectsOnlyTheNamedDLCs locks --dlc in both of its spellings: by
// product id, which needs no title, and by title, which does.
func TestBuildPlanSelectsOnlyTheNamedDLCs(t *testing.T) {
	cases := []struct {
		name     string
		selector string
	}{
		{"by product id", dlcATestID},
		{"by title", dlcATestTitle},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, cfg := newDLCFixture(t)
			cfg.DownloadConfig.DLCSelectors = []config.DLCSelector{{Value: c.selector}}
			d := newOfflineDownloader(t, f.Server, cfg, newFakeConsole())

			res, err := d.BuildPlan(context.Background(), NewInstallRequest(cfg, planProductID, "", ProductRefExact))
			if err != nil {
				t.Fatalf("BuildPlan: %v", err)
			}
			paths := plannedPaths(res)
			if !paths["game/base.bin"] || !paths["game/dlc_a.bin"] {
				t.Errorf("plan = %v, want the base game and the named DLC", paths)
			}
			if paths["game/dlc_b.bin"] {
				t.Error("a DLC that was not named must not be planned")
			}
		})
	}
}

// TestBuildPlanExcludesTheNamedDLC locks --exclude-dlc: everything else stays.
func TestBuildPlanExcludesTheNamedDLC(t *testing.T) {
	f, cfg := newDLCFixture(t)
	cfg.DownloadConfig.DLCSelectors = []config.DLCSelector{{Value: dlcBTestID, Exclude: true}}
	d := newOfflineDownloader(t, f.Server, cfg, newFakeConsole())

	res, err := d.BuildPlan(context.Background(), NewInstallRequest(cfg, planProductID, "", ProductRefExact))
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	paths := plannedPaths(res)
	if !paths["game/base.bin"] || !paths["game/dlc_a.bin"] {
		t.Errorf("plan = %v, want the base game and the DLC that was not excluded", paths)
	}
	if paths["game/dlc_b.bin"] {
		t.Error("the excluded DLC must not be planned")
	}
}

// TestBuildPlanRefusesAnUnownedDLC locks the asymmetry that matters: --dlc is a
// promise, so an unowned one is refused before anything is written rather than
// reported as an install that succeeded without it.
func TestBuildPlanRefusesAnUnownedDLC(t *testing.T) {
	f, cfg := newDLCFixture(t)
	f.setStatus("/products/"+dlcATestID+"/secure_link", http.StatusForbidden)
	cfg.DownloadConfig.DLCSelectors = []config.DLCSelector{{Value: dlcATestID}}
	d := newOfflineDownloader(t, f.Server, cfg, newFakeConsole())

	res, err := d.BuildPlan(context.Background(), NewInstallRequest(cfg, planProductID, "", ProductRefExact))
	if err == nil {
		t.Fatal("BuildPlan must refuse an unowned --dlc")
	}
	if _, ok := errors.AsType[*UsageError](err); !ok {
		t.Errorf("err = %v, want a usage-class refusal", err)
	}
	for _, want := range []string{"not owned by this account", dlcATestID, dlcATestTitle} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want it to mention %q", err, want)
		}
	}
	if len(res.Plan.Tasks) != 0 {
		t.Errorf("a refused selection must plan nothing, got %d tasks", len(res.Plan.Tasks))
	}
}

// TestBuildPlanAllowsExcludingAnUnownedDLC locks the other half of the
// asymmetry: an unowned DLC is already dropped by the entitlement filter, so
// excluding it is a no-op rather than an error.
func TestBuildPlanAllowsExcludingAnUnownedDLC(t *testing.T) {
	f, cfg := newDLCFixture(t)
	f.setStatus("/products/"+dlcATestID+"/secure_link", http.StatusForbidden)
	cfg.DownloadConfig.DLCSelectors = []config.DLCSelector{{Value: dlcATestID, Exclude: true}}
	d := newOfflineDownloader(t, f.Server, cfg, newFakeConsole())

	res, err := d.BuildPlan(context.Background(), NewInstallRequest(cfg, planProductID, "", ProductRefExact))
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	paths := plannedPaths(res)
	if !paths["game/base.bin"] || !paths["game/dlc_b.bin"] {
		t.Errorf("plan = %v, want the base game and the owned DLC", paths)
	}
	if paths["game/dlc_a.bin"] {
		t.Error("the unowned DLC must not be planned")
	}
}

// TestBuildPlanRefusesAContradiction locks the conflict rule: the same DLC named
// both ways is an error, and it is detected on the resolved product id, so an id
// and a title naming one DLC still conflict.
func TestBuildPlanRefusesAContradiction(t *testing.T) {
	cases := []struct {
		name      string
		selectors []config.DLCSelector
	}{
		{"the same id both ways", []config.DLCSelector{
			{Value: dlcATestID}, {Value: dlcATestID, Exclude: true},
		}},
		{"an id and a title naming one DLC", []config.DLCSelector{
			{Value: dlcATestTitle}, {Value: dlcATestID, Exclude: true},
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, cfg := newDLCFixture(t)
			cfg.DownloadConfig.DLCSelectors = c.selectors
			d := newOfflineDownloader(t, f.Server, cfg, newFakeConsole())

			_, err := d.BuildPlan(context.Background(), NewInstallRequest(cfg, planProductID, "", ProductRefExact))
			if err == nil {
				t.Fatal("BuildPlan must refuse a contradiction")
			}
			if _, ok := errors.AsType[*UsageError](err); !ok {
				t.Errorf("err = %v, want a usage-class refusal", err)
			}
			if !strings.Contains(err.Error(), "both selected and excluded") {
				t.Errorf("err = %v, want it to name the contradiction", err)
			}
		})
	}
}

// TestBuildPlanRefusesAnAmbiguousName locks that two DLCs sharing a title are
// ambiguous by definition, and that the report names both so the user can pick
// an id instead.
func TestBuildPlanRefusesAnAmbiguousName(t *testing.T) {
	f, cfg := newDLCFixture(t)
	// The second DLC takes the first one's title, so the title names both.
	f.set("/products/"+dlcBTestID, `{"id":"`+dlcBTestID+`","title":"`+dlcATestTitle+`"}`)
	cfg.DownloadConfig.DLCSelectors = []config.DLCSelector{{Value: dlcATestTitle}}
	d := newOfflineDownloader(t, f.Server, cfg, newFakeConsole())

	_, err := d.BuildPlan(context.Background(), NewInstallRequest(cfg, planProductID, "", ProductRefExact))
	if err == nil {
		t.Fatal("BuildPlan must refuse an ambiguous title")
	}
	for _, want := range []string{"is ambiguous", dlcATestID, dlcBTestID} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want it to mention %q", err, want)
		}
	}
}

// TestBuildPlanReportsEverySelectorProblemInInputOrder locks the failure model:
// all problems are collected and reported together, in the order the user typed
// them, because the run has not started and one edit can fix them all.
func TestBuildPlanReportsEverySelectorProblemInInputOrder(t *testing.T) {
	f, cfg := newDLCFixture(t)
	f.setStatus("/products/"+dlcBTestID+"/secure_link", http.StatusForbidden)
	cfg.DownloadConfig.DLCSelectors = []config.DLCSelector{
		{Value: "klingon"},                 // unknown, first
		{Value: dlcBTestID},                // not owned, second
		{Value: dlcBTestID, Exclude: true}, // contradicts the second, reported at its own position
	}
	d := newOfflineDownloader(t, f.Server, cfg, newFakeConsole())

	_, err := d.BuildPlan(context.Background(), NewInstallRequest(cfg, planProductID, "", ProductRefExact))
	if err == nil {
		t.Fatal("BuildPlan must report the selector problems")
	}
	text := err.Error()
	if !strings.Contains(text, "cannot use the selected DLCs") {
		t.Errorf("err = %v, want it to head the report", err)
	}
	unknown := strings.Index(text, "klingon")
	notOwned := strings.Index(text, "not owned by this account")
	if unknown < 0 || notOwned < 0 {
		t.Fatalf("err = %v, want both problems reported at once", err)
	}
	if unknown > notOwned {
		t.Errorf("err = %v, want the problems in selector input order", err)
	}
}

// TestInstallOptionsListsDLCs locks the discovery surface: the listing reports
// every DLC the build carries, its title, and whether the account owns it —
// built from the same discovery the selectors resolve against.
func TestInstallOptionsListsDLCs(t *testing.T) {
	f, cfg := newDLCFixture(t)
	f.setStatus("/products/"+dlcBTestID+"/secure_link", http.StatusForbidden)
	d := newOfflineDownloader(t, f.Server, cfg, newFakeConsole())

	res, err := d.InstallOptions(context.Background(), planProductID, ProductRefExact, "windows", "")
	if err != nil {
		t.Fatalf("InstallOptions: %v", err)
	}
	if len(res.DLCs) != 2 {
		t.Fatalf("DLCs = %+v, want both products", res.DLCs)
	}
	want := map[string]struct {
		title string
		owned bool
	}{
		dlcATestID: {dlcATestTitle, true},
		dlcBTestID: {dlcBTestTitle, false},
	}
	for _, dlc := range res.DLCs {
		expect, ok := want[dlc.ProductID]
		if !ok {
			t.Errorf("unexpected DLC %+v", dlc)
			continue
		}
		if dlc.Title != expect.title || dlc.Owned != expect.owned {
			t.Errorf("DLC %s = %+v, want title %q owned %v", dlc.ProductID, dlc, expect.title, expect.owned)
		}
	}
}
