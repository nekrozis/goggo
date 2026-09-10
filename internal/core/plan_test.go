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
)

// Distinct 40-hex digests for the fixture; the depot manifests are addressed by
// the galaxy path their hash expands to.
const (
	planProductID    = "1495134320"
	planDLCProductID = "555000111"
	planDepProductID = "777"

	planBuildHashNew  = "1111111111111111111111111111111111111111"
	planBuildHashOld  = "2222222222222222222222222222222222222222"
	planDepotHashLang = "3333333333333333333333333333333333333333"
	planDepotHashDLC  = "4444444444444444444444444444444444444444"
	planDepotHashDep  = "5555555555555555555555555555555555555555"
	planOldDepotHash  = "6666666666666666666666666666666666666666"
)

func planChunk(compressed, uncompressed string) string {
	return `{"md5_compressed":"` + compressed + `","md5_uncompressed":"` + uncompressed + `","compressedSize":10,"size":20}`
}

// planFixture serves the build, manifest and dependency documents BuildPlan
// reads, keyed by request path.
type planFixture struct {
	*httptest.Server

	mu       sync.Mutex
	requests []string
	bodies   map[string]string
}

func newPlanFixture(t *testing.T) *planFixture {
	t.Helper()
	f := &planFixture{bodies: map[string]string{}}

	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.requests = append(f.requests, r.URL.Path)
		body, ok := f.bodies[r.URL.Path]
		f.mu.Unlock()

		if !ok {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, body)
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *planFixture) set(path, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bodies[path] = body
}

func (f *planFixture) seen(want string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	var n int
	for _, path := range f.requests {
		if strings.Contains(path, want) {
			n++
		}
	}
	return n
}

func (f *planFixture) setDefaultBodies() {
	v2New := galaxy.HashToGalaxyPath(planBuildHashNew)
	v2Old := galaxy.HashToGalaxyPath(planBuildHashOld)
	v2DLC := galaxy.HashToGalaxyPath(planDepotHashDLC)
	v2Dep := galaxy.HashToGalaxyPath(planDepotHashDep)
	v2OldDepot := galaxy.HashToGalaxyPath(planOldDepotHash)

	f.set("/products/"+planProductID+"/os/windows/builds",
		`{"items":[`+
			`{"build_id":"b-new","version_name":"1.0.2","date_published":"2024-03-02","generation":2,`+
			`"link":"https://cdn.gog.com/content-system/v2/meta/`+v2New+`"},`+
			`{"build_id":"b-old","version_name":"1.0.1","date_published":"2024-01-05","generation":2,`+
			`"link":"https://cdn.gog.com/content-system/v2/meta/`+v2Old+`"}]}`)

	f.set("/content-system/v2/meta/"+v2New,
		`{"baseProductId":"`+planProductID+`","installDirectory":"W3 GOTY","version":2,`+
			`"products":[{"name":"The Witcher 3: Wild Hunt"}],`+
			`"dependencies":["dep1"],`+
			`"depots":[`+
			`{"productId":"`+planProductID+`","languages":["en-US"],"osBitness":["64"],"manifest":"`+planDepotHashLang+`"},`+
			`{"productId":"`+planDLCProductID+`","languages":["en-US"],"osBitness":["64"],"manifest":"`+planDepotHashDLC+`"}]}`)

	// The base depot: one regular file, one file inside the small-files
	// container, and the container itself with its own chunks.
	// The base depot: one regular file, one file inside the small-files
	// container, and the container itself with its own chunks.
	f.set("/content-system/v2/meta/"+galaxy.HashToGalaxyPath(planDepotHashLang),
		`{"depot":{`+
			`"smallFilesContainer":{"chunks":[`+planChunk("sfc-c", "sfc-u")+`]},`+
			`"items":[`+
			`{"path":"game/data.bin","md5":"base-md5","chunks":[`+planChunk("c1c", "c1u")+`]},`+
			`{"path":"game/small1.txt","sfcRef":{"offset":0,"size":10},"chunks":[`+planChunk("c2c", "c2u")+`]}]}}`)

	// The DLC depot re-declares the same path with a different md5, so the
	// dedup step must replace the base game's entry with it.
	f.set("/content-system/v2/meta/"+v2DLC,
		`{"depot":{"items":[`+
			`{"path":"game/data.bin","md5":"dlc-md5","chunks":[`+planChunk("c3c", "c3u")+`]}]}}`)

	f.set("/content-system/v2/meta/"+v2Old,
		`{"baseProductId":"`+planProductID+`","installDirectory":"W3 GOTY","version":2,`+
			`"products":[{"name":"old"}],`+
			`"depots":[{"productId":"`+planProductID+`","languages":["en-US"],"osBitness":["64"],"manifest":"`+planOldDepotHash+`"}]}`)

	f.set("/content-system/v2/meta/"+v2OldDepot,
		`{"depot":{"items":[`+
			`{"path":"game/data.bin","md5":"base-md5","chunks":[`+planChunk("c1c", "c1u")+`]},`+
			`{"path":"game/oldfile.bin","md5":"old-md5","chunks":[`+planChunk("c4c", "c4u")+`]}]}}`)

	f.set("/dependencies/repository",
		`{"repository_manifest":"https://content-system.gog.com/dep/repo-manifest"}`)
	f.set("/dep/repo-manifest",
		`{"depots":[{"dependencyId":"dep1","productId":"`+planDepProductID+`",`+
			`"languages":["en-US"],"osBitness":["64"],"manifest":"`+planDepotHashDep+`"}]}`)
	f.set("/content-system/v2/dependencies/meta/"+v2Dep,
		`{"depot":{"items":[`+
			`{"path":"game/dep/depfile.bin","md5":"dep-md5","chunks":[`+planChunk("c5c", "c5u")+`]}]}}`)
}

func planTestConfig(t *testing.T) config.Config {
	t.Helper()
	dir := t.TempDir()
	cfg := config.NewConfig(dir, dir)
	// Parse owns the trailing-separator normalisation and the subdirectory
	// default; the test applies both so the plan sees production shapes.
	cfg.Directories.Directory = dir + "/"
	cfg.Directories.SubDirectories = true
	// The subdirectory template is a Parse-time default (review D35/Delta 5):
	// config.NewConfig does not carry it, so the test supplies the value Parse
	// would.
	cfg.Directories.GalaxyInstallSubdir = "%install_dir%"
	cfg.DownloadConfig.GalaxyPlatform = config.PlatformWindows
	cfg.DownloadConfig.GalaxyLanguage = config.LangEN
	cfg.DownloadConfig.GalaxyArch = config.ArchX64
	cfg.DownloadConfig.GalaxyDependencies = true
	cfg.GalaxyBuildSortingOrder = "none"
	cfg.BlacklistFilePath = filepath.Join(dir, "blacklist.txt")
	return cfg
}

// TestBuildPlanFullChain walks the whole plan: builds, the build manifest, both
// depots, the dependency repository, the SFC decision and the old-build diff —
// and locks the plan's contents and order.
func TestBuildPlanFullChain(t *testing.T) {
	f := newPlanFixture(t)
	f.setDefaultBodies()
	cfg := planTestConfig(t)
	d := newOfflineDownloader(t, f.Server, cfg, newFakeConsole())

	// The previously installed build recorded itself in the info file, which
	// the old-build diff reads.
	installPath := cfg.Directories.Directory + "W3 GOTY"
	if err := os.MkdirAll(installPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installPath+"/goggame-"+planProductID+".info",
		[]byte(`{"buildId":"b-old"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	req := NewInstallRequest(cfg, planProductID, "")
	res, err := d.BuildPlan(context.Background(), req)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}

	// The dedup put the DLC's file over the base game's, the dependency added
	// its file, and the small-files container downloads as one task; the file
	// inside the container waits for extraction. Paths come out sorted.
	wantPaths := []string{
		"galaxy_smallfilescontainer_" + planProductID,
		"game/data.bin",
		"game/dep/depfile.bin",
	}
	if len(res.Plan.Tasks) != len(wantPaths) {
		t.Fatalf("tasks = %d, want %d", len(res.Plan.Tasks), len(wantPaths))
	}
	for i, want := range wantPaths {
		if got := res.Plan.Tasks[i].Item.Path; got != want {
			t.Errorf("tasks[%d].Path = %q, want %q", i, got, want)
		}
		if !strings.HasPrefix(res.Plan.Tasks[i].Destination, cfg.Directories.Directory) {
			t.Errorf("tasks[%d].Destination = %q, want it under the install path",
				i, res.Plan.Tasks[i].Destination)
		}
	}
	// The dedup replaced the base entry with the DLC's.
	if got := res.Plan.Tasks[1].Item; got.ProductID != planDLCProductID || got.MD5 != "dlc-md5" {
		t.Errorf("data.bin = %+v, want the DLC entry", got)
	}
	// The dependency entry kept its own product id and flag.
	if got := res.Plan.Tasks[2].Item; !got.IsDependency || got.ProductID != planDepProductID {
		t.Errorf("depfile = %+v", got)
	}

	// The container is a task; the file inside it is plan data for the
	// extraction step, not a download target (review D50).
	if len(res.Plan.SFC) != 1 {
		t.Fatalf("SFC groups = %d, want 1", len(res.Plan.SFC))
	}
	if res.Plan.SFC[0].Container.Path != "galaxy_smallfilescontainer_"+planProductID {
		t.Errorf("SFC container = %q", res.Plan.SFC[0].Container.Path)
	}
	if len(res.Plan.SFC[0].Items) != 1 || res.Plan.SFC[0].Items[0].Path != "game/small1.txt" {
		t.Errorf("SFC items = %+v", res.Plan.SFC[0].Items)
	}
	for _, task := range res.Plan.Tasks {
		if task.Item.IsInSFC {
			t.Errorf("%q is inside the container and must not be a download task", task.Item.Path)
		}
	}

	// The old build's gone file is plan data, and its message precedes nothing:
	// it is emitted whether or not the file is on disk.
	wantDelete := cfg.Directories.Directory + "W3 GOTY/game/oldfile.bin"
	if len(res.Plan.Deletes) != 1 || res.Plan.Deletes[0] != wantDelete {
		t.Errorf("deletes = %v, want [%s]", res.Plan.Deletes, wantDelete)
	}
	var sawDelete bool
	for _, m := range res.Messages {
		if m.Text == "Deleting "+wantDelete {
			sawDelete = true
		}
	}
	if !sawDelete {
		t.Errorf("messages = %+v, want the Deleting line", res.Messages)
	}

	// The summary lines (downloader.cpp:4212-4214).
	var sawTitle, sawCount, sawSize bool
	for _, m := range res.Messages {
		switch {
		case m.Text == "The Witcher 3: Wild Hunt":
			sawTitle = true
		case m.Text == "Files: 3":
			sawCount = true
		case strings.HasPrefix(m.Text, "Total size installed: "):
			sawSize = true
		}
	}
	if !sawTitle || !sawCount || !sawSize {
		t.Errorf("summary messages incomplete: %+v", res.Messages)
	}

	// The chain touched every stage in order: builds, the build manifest, both
	// depot manifests, the dependency repository twice, the dependency depot.
	for _, want := range []string{
		"/products/" + planProductID + "/os/windows/builds",
		"/content-system/v2/meta/" + galaxy.HashToGalaxyPath(planBuildHashNew),
		"/content-system/v2/meta/" + galaxy.HashToGalaxyPath(planDepotHashLang),
		"/content-system/v2/meta/" + galaxy.HashToGalaxyPath(planDepotHashDLC),
		"/dependencies/repository",
		"/dep/repo-manifest",
		"/content-system/v2/dependencies/meta/" + galaxy.HashToGalaxyPath(planDepotHashDep),
	} {
		if f.seen(want) == 0 {
			t.Errorf("the chain never requested %s", want)
		}
	}
}

// TestBuildPlanContainerDroppedWhenInstalled locks the SFC decision: a file
// that lives in the container and is already on disk drops the container, and
// the files download normally instead.
func TestBuildPlanContainerDroppedWhenInstalled(t *testing.T) {
	f := newPlanFixture(t)
	f.setDefaultBodies()
	cfg := planTestConfig(t)
	d := newOfflineDownloader(t, f.Server, cfg, newFakeConsole())

	installPath := cfg.Directories.Directory + "W3 GOTY"
	if err := os.MkdirAll(installPath+"/game", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installPath+"/game/small1.txt", []byte("installed"), 0o644); err != nil {
		t.Fatal(err)
	}

	req := NewInstallRequest(cfg, planProductID, "")
	res, err := d.BuildPlan(context.Background(), req)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}

	if len(res.Plan.SFC) != 0 {
		t.Errorf("SFC groups = %d, want none once a member is installed", len(res.Plan.SFC))
	}
	for _, task := range res.Plan.Tasks {
		if task.Item.IsSmallFilesContainer {
			t.Errorf("%q must be dropped with the container", task.Item.Path)
		}
		if task.Item.Path == "game/small1.txt" {
			return // the member downloads normally: found it
		}
	}
	t.Error("small1.txt must download normally when the container is dropped")
}

// TestBuildPlanGenerationGate locks that a non-generation-2 build stops with
// the message alone: an empty plan and no error, because main.cpp never folds
// this message into the exit code.
func TestBuildPlanGenerationGate(t *testing.T) {
	f := newPlanFixture(t)
	f.setDefaultBodies()
	f.set("/products/"+planProductID+"/os/windows/builds",
		`{"items":[{"build_id":"b-1","generation":1,"link":"https://cdn.gog.com/x"}]}`)
	cfg := planTestConfig(t)
	d := newOfflineDownloader(t, f.Server, cfg, newFakeConsole())

	res, err := d.BuildPlan(context.Background(), NewInstallRequest(cfg, planProductID, ""))
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if len(res.Messages) != 1 || res.Messages[0].Text != msgGenerationsOneTwo {
		t.Errorf("messages = %+v, want the generation message", res.Messages)
	}
	if len(res.Plan.Tasks) != 0 {
		t.Errorf("tasks = %+v, want none", res.Plan.Tasks)
	}
}

// TestBuildPlanLinuxFallback locks the not-ported fallback: the two support
// messages and an error that names the missing engine, not a silent success.
func TestBuildPlanLinuxFallback(t *testing.T) {
	f := newPlanFixture(t)
	f.set("/products/"+planProductID+"/os/linux/builds", `{}`)
	cfg := planTestConfig(t)
	cfg.DownloadConfig.GalaxyPlatform = config.PlatformLinux
	d := newOfflineDownloader(t, f.Server, cfg, newFakeConsole())

	req := NewInstallRequest(cfg, planProductID, "")
	res, err := d.BuildPlan(context.Background(), req)
	if err == nil || !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("err = %v, want ErrNotImplemented", err)
	}
	var texts []string
	for _, m := range res.Messages {
		texts = append(texts, m.Text)
	}
	joined := strings.Join(texts, "\n")
	if !strings.Contains(joined, msgNoLinuxSupport) || !strings.Contains(joined, msgCheckInstallers) {
		t.Errorf("messages = %+v, want the two Linux support lines", res.Messages)
	}
}

// TestBuildPlanFreeSpaceGate locks the option's effect: with --check-free-space
// a plan whose uncompressed total cannot fit fails with the space error, and
// the same plan passes when the option is off.
func TestBuildPlanFreeSpaceGate(t *testing.T) {
	f := newPlanFixture(t)
	f.setDefaultBodies()
	cfg := planTestConfig(t)
	cfg.DownloadConfig.FreeSpaceCheck = true

	// A total no real volume satisfies — 1<<62 bytes is exactly representable
	// in float64, so the depot item carries it losslessly — makes the gate
	// fire without mocking the disk query.
	huge := `{"depot":{"items":[` +
		`{"path":"game/data.bin","md5":"dlc-md5","chunks":[` +
		`{"md5_compressed":"c3c","md5_uncompressed":"c3u","compressedSize":4611686018427387904,"size":4611686018427387904}]}]}}`
	f.set("/content-system/v2/meta/"+galaxy.HashToGalaxyPath(planDepotHashDLC), huge)

	d := newOfflineDownloader(t, f.Server, cfg, newFakeConsole())
	res, err := d.BuildPlan(context.Background(), NewInstallRequest(cfg, planProductID, ""))
	if err == nil || !strings.Contains(err.Error(), "not enough free space") {
		t.Fatalf("err = %v, want the free-space failure", err)
	}
	if len(res.Plan.Tasks) != 0 {
		t.Errorf("tasks = %+v, want none on a failed gate", res.Plan.Tasks)
	}

	// Without the option the same plan is built silently. The flag rides in
	// the Downloader's configuration copy, so it is flipped before a fresh
	// Downloader is constructed.
	cfg.DownloadConfig.FreeSpaceCheck = false
	d2 := newOfflineDownloader(t, f.Server, cfg, newFakeConsole())
	if _, err := d2.BuildPlan(context.Background(), NewInstallRequest(cfg, planProductID, "")); err != nil {
		t.Fatalf("BuildPlan with the gate off: %v", err)
	}
}

// TestBuildPlanBlacklistFiltersTasks locks that a blacklisted planned file
// drops out of the tasks and reports itself when the run is verbose.
func TestBuildPlanBlacklistFiltersTasks(t *testing.T) {
	f := newPlanFixture(t)
	f.setDefaultBodies()
	cfg := planTestConfig(t)
	cfg.MsgLevel = msgLevelVerbose
	blPath := filepath.Join(t.TempDir(), "blacklist.txt")
	if err := os.WriteFile(blPath, []byte("R \\.bin$\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg.BlacklistFilePath = blPath
	d := newOfflineDownloader(t, f.Server, cfg, newFakeConsole())

	res, err := d.BuildPlan(context.Background(), NewInstallRequest(cfg, planProductID, ""))
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	for _, task := range res.Plan.Tasks {
		if strings.HasSuffix(task.Item.Path, ".bin") {
			t.Errorf("%q is blacklisted and must not be a task", task.Item.Path)
		}
	}
	var sawSkip bool
	for _, m := range res.Messages {
		if strings.Contains(m.Text, "Skipping blacklisted file:") {
			sawSkip = true
		}
	}
	if !sawSkip {
		t.Errorf("messages = %+v, want the verbose skip line", res.Messages)
	}

	// The file must have been dropped, not kept: only the container and the
	// dependency file remain.
	if len(res.Plan.Tasks) != 1 {
		t.Errorf("tasks = %d, want the two .bin tasks gone", len(res.Plan.Tasks))
	}
}
