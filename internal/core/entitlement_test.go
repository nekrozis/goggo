package core

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/nekrozis/goggo/internal/config"
	"github.com/nekrozis/goggo/internal/galaxy"
	"github.com/nekrozis/goggo/internal/model"
)

// TestBuildPlanDropsUnownedDLC locks the entitlement boundary: a DLC product
// whose secure link answers 403 leaves the plan entirely — no task, no
// expected file — and one summary line reports it, while the base product is
// unaffected.
func TestBuildPlanDropsUnownedDLC(t *testing.T) {
	f := newPlanFixture(t)
	f.setDefaultBodies()
	f.setStatus("/products/"+planDLCProductID+"/secure_link", http.StatusForbidden)
	cfg := planTestConfig(t)
	d := newOfflineDownloader(t, f.Server, cfg, newFakeConsole())

	res, err := d.BuildPlan(context.Background(), NewInstallRequest(cfg, planProductID, "", ProductRefExact))
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}

	for _, task := range res.Plan.Tasks {
		if task.Item.ProductID == planDLCProductID {
			t.Errorf("unowned DLC product is still planned: %s", task.Item.Path)
		}
	}
	for _, it := range res.Expected {
		if it.Item.ProductID == planDLCProductID {
			t.Errorf("unowned DLC product is still expected: %s", it.Destination)
		}
	}

	// The DLC re-declared game/data.bin with its own md5; with the product
	// dropped, the base game's entry must be the one that survives.
	var data *model.FileTask
	for i := range res.Plan.Tasks {
		if res.Plan.Tasks[i].Item.Path == "game/data.bin" {
			data = &res.Plan.Tasks[i]
		}
	}
	if data == nil {
		t.Fatal("game/data.bin is missing from the plan")
	}
	if data.Item.MD5 != "base-md5" || data.Item.ProductID != planProductID {
		t.Errorf("game/data.bin = %+v, want the base entry", data.Item)
	}

	summary := ""
	for _, m := range res.Messages {
		if strings.Contains(m.Text, "not owned by this account") {
			summary = m.Text
		}
	}
	if summary == "" {
		t.Fatalf("no summary line for the dropped product: %+v", res.Messages)
	}
	if !strings.Contains(summary, "Skipping 1 DLC products") || !strings.Contains(summary, planDLCProductID) {
		t.Errorf("summary = %q, want the dropped product id and its count", summary)
	}

	if got := f.seen("/products/" + planDLCProductID + "/secure_link"); got != 1 {
		t.Errorf("unowned product requested %d times, want exactly 1", got)
	}
}

// TestBuildPlanBaseNotLicensedIsFatal locks the other half of the boundary: an
// unowned base product fails the plan instead of producing a plan whose
// transfers would all 403.
func TestBuildPlanBaseNotLicensedIsFatal(t *testing.T) {
	f := newPlanFixture(t)
	f.setDefaultBodies()
	f.setStatus("/products/"+planProductID+"/secure_link", http.StatusForbidden)
	cfg := planTestConfig(t)
	d := newOfflineDownloader(t, f.Server, cfg, newFakeConsole())

	_, err := d.BuildPlan(context.Background(), NewInstallRequest(cfg, planProductID, "", ProductRefExact))
	if err == nil {
		t.Fatal("BuildPlan succeeded for an unowned base product")
	}
	if !isNotLicensed(err) {
		t.Errorf("err = %v, want it to wrap ErrNotLicensed", err)
	}
}

// TestBuildPlanProbesEachProductOnce locks the run-scoped cache: the current
// build, the old-build diff and the transfer all resolve through one resolver,
// so a product is probed once per run rather than once per caller.
func TestBuildPlanProbesEachProductOnce(t *testing.T) {
	f := newPlanFixture(t)
	f.setDefaultBodies()
	cfg := planTestConfig(t)
	d := newOfflineDownloader(t, f.Server, cfg, newFakeConsole())

	// The previously installed build makes planForInstall run the old-build
	// diff, which resolves the old manifest's depots a second time.
	installPath := filepath.Join(cfg.Directories.Directory, "W3 GOTY")
	if err := os.MkdirAll(installPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installPath+"/goggame-"+planProductID+".info",
		[]byte(`{"buildId":"b-old"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := d.BuildPlan(context.Background(), NewInstallRequest(cfg, planProductID, "", ProductRefExact)); err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}

	for _, productID := range []string{planProductID, planDLCProductID} {
		path := "/products/" + productID + "/secure_link"
		if got := f.seen(path); got != 1 {
			t.Errorf("%s requested %d times, want 1", path, got)
		}
	}
}

// TestBuildPlanExcludedDLCSkipsProbe locks the cost boundary: when the include
// mask already excludes DLC, no DLC product is probed at all.
func TestBuildPlanExcludedDLCSkipsProbe(t *testing.T) {
	f := newPlanFixture(t)
	f.setDefaultBodies()
	cfg := planTestConfig(t)
	cfg.DownloadConfig.Include &^= config.GFDLC
	d := newOfflineDownloader(t, f.Server, cfg, newFakeConsole())

	res, err := d.BuildPlan(context.Background(), NewInstallRequest(cfg, planProductID, "", ProductRefExact))
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}

	for _, task := range res.Plan.Tasks {
		if task.Item.ProductID == planDLCProductID {
			t.Errorf("a DLC task survived the exclusion mask: %s", task.Item.Path)
		}
	}
	if got := f.seen("/products/" + planDLCProductID + "/secure_link"); got != 0 {
		t.Errorf("DLC product probed %d times under an DLC exclusion mask, want 0", got)
	}
	if got := f.seen("/products/" + planProductID + "/secure_link"); got != 1 {
		t.Errorf("base product probed %d times, want 1", got)
	}
}

// TestLinkResolverTransientFailureIsNotCached locks the caching rule: a
// failure that is not the entitlement answer is returned but not remembered,
// so the next caller retries instead of inheriting it as "unowned".
func TestLinkResolverTransientFailureIsNotCached(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		requests++
		first := requests == 1
		mu.Unlock()
		if first {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		fmt.Fprint(w, ownedLinkBody)
	}))
	t.Cleanup(srv.Close)

	cfg := planTestConfig(t)
	cfg.Retries = 0
	d := newOfflineDownloader(t, srv, cfg, newFakeConsole())

	if _, err := d.links.product(context.Background(), "42"); err == nil {
		t.Fatal("first call succeeded, want the transient failure")
	}
	res, err := d.links.product(context.Background(), "42")
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if !res.owned || len(res.templates) == 0 {
		t.Fatalf("second call = %+v, want owned with templates", res)
	}

	mu.Lock()
	got := requests
	mu.Unlock()
	if got != 2 {
		t.Errorf("requests = %d, want 2: a transient failure must not be cached", got)
	}
}

// TestLinkResolverCoalescesConcurrentCalls locks the single-flight rule: many
// workers asking for one product at the same time produce one request, and all
// of them observe the same answer.
func TestLinkResolverCoalescesConcurrentCalls(t *testing.T) {
	arrived := make(chan struct{}, 1)
	release := make(chan struct{})
	var mu sync.Mutex
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		select {
		case arrived <- struct{}{}:
		default:
		}
		<-release
		fmt.Fprint(w, ownedLinkBody)
	}))
	t.Cleanup(srv.Close)

	cfg := planTestConfig(t)
	cfg.Retries = 0
	d := newOfflineDownloader(t, srv, cfg, newFakeConsole())

	const workers = 8
	var wg sync.WaitGroup
	results := make([]linkResult, workers)
	errs := make([]error, workers)
	for i := range workers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = d.links.product(context.Background(), "42")
		}(i)
	}

	<-arrived
	close(release)
	wg.Wait()

	for i := range results {
		if errs[i] != nil {
			t.Fatalf("worker %d: %v", i, errs[i])
		}
		if !results[i].owned || len(results[i].templates) == 0 {
			t.Fatalf("worker %d result = %+v, want owned with templates", i, results[i])
		}
	}
	mu.Lock()
	got := requests
	mu.Unlock()
	if got != 1 {
		t.Errorf("requests = %d, want 1 for one product", got)
	}
}

// TestLinkResolverCachesEntitlementAnswer locks the positive half of the
// caching rule: a settled answer is served from the cache, so a second call
// costs no request.
func TestLinkResolverCachesEntitlementAnswer(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		fmt.Fprint(w, ownedLinkBody)
	}))
	t.Cleanup(srv.Close)

	cfg := planTestConfig(t)
	cfg.Retries = 0
	d := newOfflineDownloader(t, srv, cfg, newFakeConsole())

	for i := range 3 {
		if _, err := d.links.product(context.Background(), "42"); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}

	mu.Lock()
	got := requests
	mu.Unlock()
	if got != 1 {
		t.Errorf("requests = %d, want 1 for three calls", got)
	}
}

// TestChunkURLProviderReusesResolverTemplates locks the transfer's reuse of
// the resolver: interleaving two products costs one request each, and the URL
// carries the chunk path built from the cached template.
func TestChunkURLProviderReusesResolverTemplates(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		fmt.Fprint(w, ownedLinkBody)
	}))
	t.Cleanup(srv.Close)

	cfg := planTestConfig(t)
	cfg.Retries = 0
	d := newOfflineDownloader(t, srv, cfg, newFakeConsole())
	p := d.chunkURLProvider()

	for _, productID := range []string{"100", "200", "100", "200"} {
		task := model.FileTask{Item: model.GalaxyDepotItem{ProductID: productID}}
		got, err := p.URL(context.Background(), task, model.GalaxyDepotItemChunk{CompressedMD5: "abc"})
		if err != nil {
			t.Fatalf("URL(%s): %v", productID, err)
		}
		want := "https://cdn.gog.com/chunks/" + galaxy.HashToGalaxyPath("abc")
		if got != want {
			t.Errorf("URL = %q, want %q", got, want)
		}
	}

	mu.Lock()
	got := requests
	mu.Unlock()
	if got != 2 {
		t.Errorf("requests = %d, want 2 for two products used twice each", got)
	}
}

// ownedLinkBody is a secure link document whose single template carries the
// galaxy path marker, so a resolved URL is "https://cdn.gog.com/chunks/<path>".
const ownedLinkBody = `{"urls":[{"endpoint_name":"cdnMain","url_format":"https://cdn.gog.com/chunks{path}","parameters":{"path":""}}]}`

// isNotLicensed reports whether err carries ErrNotLicensed.
func isNotLicensed(err error) bool {
	return errors.Is(err, ErrNotLicensed)
}
