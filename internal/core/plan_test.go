package core

import (
	"context"
	"crypto/md5"
	"encoding/hex"
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
	return `{"compressedMd5":"` + compressed + `","md5":"` + uncompressed + `","compressedSize":10,"size":20}`
}

// planFixture serves the build, manifest and dependency documents BuildPlan
// reads, keyed by request path.
type planFixture struct {
	*httptest.Server

	mu       sync.Mutex
	requests []string
	bodies   map[string]string
	holds    map[string]heldBody
}

// heldBody is a response the fixture serves in two halves, waiting for the
// test between them. It gives a test a deterministic window in which a
// transfer is provably in flight.
type heldBody struct {
	body    string
	release <-chan struct{}
}

func newPlanFixture(t *testing.T) *planFixture {
	t.Helper()
	f := &planFixture{bodies: map[string]string{}, holds: map[string]heldBody{}}

	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.requests = append(f.requests, r.URL.Path)
		held, isHeld := f.holds[r.URL.Path]
		body, ok := f.bodies[r.URL.Path]
		f.mu.Unlock()

		if isHeld {
			half := len(held.body) / 2
			fmt.Fprint(w, held.body[:half])
			if fl, isFlusher := w.(http.Flusher); isFlusher {
				fl.Flush()
			}
			<-held.release
			fmt.Fprint(w, held.body[half:])
			return
		}
		if !ok {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, body)
	}))
	t.Cleanup(f.Close)
	return f
}

// hold makes the fixture serve path in two halves, releasing the second one
// when the test closes the channel.
func (f *planFixture) hold(path, body string, release <-chan struct{}) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.holds[path] = heldBody{body: body, release: release}
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
	// The subdirectory template is a Parse-time default (D35):
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
	// extraction step, not a download target (D50).
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

	// The summary lines.
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
// the message alone: an empty plan and no error, because never folds
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

// TestBuildPlanLinuxFallback locks the unimplemented fallback: the two support
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
		`{"compressedMd5":"c3c","md5":"c3u","compressedSize":4611686018427387904,"size":4611686018427387904}]}]}}`
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

// planMessageHas reports whether one recorded plan message carries every token.
// The tokens are the contract — the decision word and the values it reports —
// and the sentence around them is not. A token with a leading space pins a
// count as a whole one, so "12 files" cannot satisfy " 2 files".
func planMessageHas(res PlanResult, tokens ...string) bool {
	for _, m := range res.Messages {
		all := true
		for _, token := range tokens {
			if !strings.Contains(m.Text, token) {
				all = false
				break
			}
		}
		if all {
			return true
		}
	}
	return false
}

// planMessageTexts joins the plan's notice lines for assertion convenience.
func planMessageTexts(res PlanResult) string {
	var b strings.Builder
	for _, m := range res.Messages {
		b.WriteString(m.Text)
		b.WriteByte('\n')
	}
	return b.String()
}

// TestBuildPlanSkipAggregation locks the plan-side skip rules: a destination
// already satisfying its item leaves the queue with NO transfer task and
// lands in the Skipped report; the per-file ": OK" is a verbose record, not a
// default notice; the header names the semantic install root exactly once; and
// a fully-up-to-date plan says "Already up to date: K files" followed by
// "Nothing to download."
func TestBuildPlanSkipAggregation(t *testing.T) {
	f := newPlanFixture(t)
	// Two single-chunk items with self-consistent digests, plus one missing
	// file: the first two are on disk already, the third must download.
	const contentA = "already on disk A"
	const contentB = "already on disk B"
	digest := func(s string) string { sum := md5.Sum([]byte(s)); return hex.EncodeToString(sum[:]) }

	v2New := galaxy.HashToGalaxyPath(planBuildHashNew)
	f.set("/products/"+planProductID+"/os/windows/builds",
		`{"items":[{"build_id":"b-new","version_name":"1.0.2","date_published":"2024-03-02","generation":2,`+
			`"link":"https://cdn.gog.com/content-system/v2/meta/`+v2New+`"}]}`)
	f.set("/content-system/v2/meta/"+v2New,
		`{"baseProductId":"`+planProductID+`","installDirectory":"W3 GOTY","version":2,`+
			`"products":[{"name":"The Witcher 3: Wild Hunt"}],`+
			`"depots":[{"productId":"`+planProductID+`","languages":["en-US"],"osBitness":["64"],"manifest":"`+planDepotHashLang+`"}]}`)
	f.set("/content-system/v2/meta/"+galaxy.HashToGalaxyPath(planDepotHashLang),
		`{"depot":{"items":[`+
			`{"path":"game/a.bin","md5":"`+digest(contentA)+`","chunks":[{"md5":"`+digest(contentA)+`","size":`+fmt.Sprint(len(contentA))+`}]},`+
			`{"path":"game/b.bin","md5":"`+digest(contentB)+`","chunks":[{"md5":"`+digest(contentB)+`","size":`+fmt.Sprint(len(contentB))+`}]},`+
			`{"path":"game/missing.bin","md5":"missing-md5","chunks":[{"md5":"m-u","size":1}]}]}}`)

	cfg := planTestConfig(t)
	d := newOfflineDownloader(t, f.Server, cfg, newFakeConsole())
	installPath := cfg.Directories.Directory + "W3 GOTY"
	if err := os.MkdirAll(installPath+"/game", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installPath+"/game/a.bin", []byte(contentA), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installPath+"/game/b.bin", []byte(contentB), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := d.BuildPlan(context.Background(), NewInstallRequest(cfg, planProductID, ""))
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if len(res.Skipped) != 2 {
		t.Errorf("Skipped = %+v, want the two installed files", res.Skipped)
	}
	if len(res.Plan.Tasks) != 1 || res.Plan.Tasks[0].Item.Path != "game/missing.bin" {
		t.Errorf("tasks = %+v, want only the missing file", res.Plan.Tasks)
	}

	msgs := planMessageTexts(res)
	if !planMessageHas(res, "Installing", installPath) {
		t.Errorf("messages = %q, want the install-root header naming %s", msgs, installPath)
	}
	if !planMessageHas(res, "Already up to date", " 2 files") {
		t.Errorf("messages = %q, want the aggregate skip count", msgs)
	}
	if strings.Contains(msgs, "a.bin: OK") || strings.Contains(msgs, "b.bin: OK") {
		t.Errorf("messages = %q, want NO per-file OK at default verbosity", msgs)
	}
	if strings.Contains(msgs, "Nothing to download") {
		t.Errorf("messages = %q, want no fast-path line while a task remains", msgs)
	}

	// Verbose restores the per-object records. The downloader copies its
	// config at construction, so the verbose run needs a fresh one.
	cfg.MsgLevel = msgLevelVerbose
	dv := newOfflineDownloader(t, f.Server, cfg, newFakeConsole())
	res, err = dv.BuildPlan(context.Background(), NewInstallRequest(cfg, planProductID, ""))
	if err != nil {
		t.Fatalf("BuildPlan verbose: %v", err)
	}
	msgs = planMessageTexts(res)
	if !strings.Contains(msgs, "game/a.bin: OK") {
		t.Errorf("verbose messages = %q, want the per-file OK line", msgs)
	}
}

// TestBuildPlanNothingToDownload locks the fully-up-to-date case: every item
// already satisfies the manifest ⇒ zero tasks, zero bytes to fetch, and the two
// aggregate sentences.
func TestBuildPlanNothingToDownload(t *testing.T) {
	f := newPlanFixture(t)
	const contentA = "already on disk A"
	digest := func(s string) string { sum := md5.Sum([]byte(s)); return hex.EncodeToString(sum[:]) }

	v2New := galaxy.HashToGalaxyPath(planBuildHashNew)
	f.set("/products/"+planProductID+"/os/windows/builds",
		`{"items":[{"build_id":"b-new","version_name":"1.0.2","date_published":"2024-03-02","generation":2,`+
			`"link":"https://cdn.gog.com/content-system/v2/meta/`+v2New+`"}]}`)
	f.set("/content-system/v2/meta/"+v2New,
		`{"baseProductId":"`+planProductID+`","installDirectory":"W3 GOTY","version":2,`+
			`"products":[{"name":"The Witcher 3: Wild Hunt"}],`+
			`"depots":[{"productId":"`+planProductID+`","languages":["en-US"],"osBitness":["64"],"manifest":"`+planDepotHashLang+`"}]}`)
	f.set("/content-system/v2/meta/"+galaxy.HashToGalaxyPath(planDepotHashLang),
		`{"depot":{"items":[`+
			`{"path":"game/a.bin","md5":"`+digest(contentA)+`","chunks":[{"md5":"`+digest(contentA)+`","size":`+fmt.Sprint(len(contentA))+`}]}]}}`)

	cfg := planTestConfig(t)
	d := newOfflineDownloader(t, f.Server, cfg, newFakeConsole())
	installPath := cfg.Directories.Directory + "W3 GOTY"
	if err := os.MkdirAll(installPath+"/game", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installPath+"/game/a.bin", []byte(contentA), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := d.BuildPlan(context.Background(), NewInstallRequest(cfg, planProductID, ""))
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if len(res.Plan.Tasks) != 0 || len(res.Skipped) != 1 {
		t.Fatalf("tasks/skipped = %d/%d, want 0 tasks and 1 skipped", len(res.Plan.Tasks), len(res.Skipped))
	}
	msgs := planMessageTexts(res)
	if !planMessageHas(res, "Already up to date", " 1 files") || !planMessageHas(res, "Nothing to download") {
		t.Errorf("messages = %q, want both aggregate lines", msgs)
	}
	if !planMessageHas(res, "Total size installed", "0.00 B") {
		t.Errorf("messages = %q, want a zero download total", msgs)
	}
}

// exact counts the recorded requests whose path is exactly want, which is what
// tells the product document apart from the build list of the same product.
func (f *planFixture) exact(want string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	var n int
	for _, path := range f.requests {
		if path == want {
			n++
		}
	}
	return n
}

// TestBuildPlanInstallDirTemplateNeedsProductInfo locks the one place the plan
// fetches a product document: a template whose value comes from the product, and
// only then. The three outcomes are the fetch itself,
// the skip when the manifest names no base product, and the templates that never
// need a document.
func TestBuildPlanInstallDirTemplateNeedsProductInfo(t *testing.T) {
	const slug = "the_witcher_3_wild_hunt"
	// The title has no colon on purpose: the plan derives the install path from it,
	// and a ":" is not a legal file name component on the development platform.
	const title = "The Witcher 3 Wild Hunt"

	productDoc := `{"id":"` + planProductID + `","slug":"` + slug + `","title":"` + title + `"}`

	// A manifest without a base product id: no request goes out and the name
	// stays literal.
	manifestWithoutBase := `{"installDirectory":"W3 GOTY","version":2,` +
		`"products":[{"name":"` + title + `"}],` +
		`"depots":[{"productId":"` + planProductID + `","languages":["en-US"],"osBitness":["64"],` +
		`"manifest":"` + planDepotHashLang + `"}]}`

	cases := []struct {
		name          string
		subdir        string
		withoutBaseID bool
		wantDirectory string
		wantProduct   int
	}{
		{name: "gamename reads the slug", subdir: "%gamename%", wantDirectory: slug, wantProduct: 1},
		{name: "title reads the title", subdir: "%title%", wantDirectory: title, wantProduct: 1},
		{
			name: "no base product id sends no request", subdir: "%gamename%", withoutBaseID: true,
			wantDirectory: "%gamename%", wantProduct: 0,
		},
		{name: "install_dir needs no document", subdir: "%install_dir%", wantDirectory: "W3 GOTY", wantProduct: 0},
		{name: "product_id needs no document", subdir: "%product_id%", wantDirectory: planProductID, wantProduct: 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newPlanFixture(t)
			f.setDefaultBodies()
			f.set("/products/"+planProductID, productDoc)
			if c.withoutBaseID {
				f.set("/content-system/v2/meta/"+galaxy.HashToGalaxyPath(planBuildHashNew), manifestWithoutBase)
			}

			cfg := planTestConfig(t)
			cfg.Directories.GalaxyInstallSubdir = c.subdir
			d := newOfflineDownloader(t, f.Server, cfg, newFakeConsole())
			// The plan refreshes an expired token before it reads the document;
			// a fresh one keeps the credential refresh (and its token file) out
			// of a test about the install directory.
			d.token.SetJSON(map[string]any{"access_token": "a", "refresh_token": "r", "expires_in": 3600})

			res, err := d.BuildPlan(context.Background(), NewInstallRequest(cfg, planProductID, ""))
			if err != nil {
				t.Fatalf("BuildPlan: %v", err)
			}
			if want := cfg.Directories.Directory + c.wantDirectory; res.InstallPath != want {
				t.Errorf("install path = %q, want %q", res.InstallPath, want)
			}
			if got := f.exact("/products/" + planProductID); got != c.wantProduct {
				t.Errorf("product document requests = %d, want %d", got, c.wantProduct)
			}
		})
	}
}
