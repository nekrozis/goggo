package core

import (
	"bytes"
	"compress/zlib"
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/nekrozis/goggo/internal/galaxy"
	"github.com/nekrozis/goggo/internal/model"
	"github.com/nekrozis/goggo/internal/transfer"
)

// chunkPayload is one chunk's fixture: the content the CDN serves compressed
// (with the md5 the plan's manifest must carry) and the decompressed content
// the installed file must end up with.
type chunkPayload struct {
	compressed string
	md5        string
	content    string
}

func planChunkPayload(t *testing.T, content string) chunkPayload {
	t.Helper()
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	if _, err := zw.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	sum := md5.Sum(buf.Bytes())
	return chunkPayload{compressed: buf.String(), md5: hex.EncodeToString(sum[:]), content: content}
}

// TestInstallEndToEnd runs Install against the fake CDN with real chunk
// payloads: the plan resolves, the deletions execute, the transfer downloads
// and assembles, and the installed files carry the decompressed content.
//
// The depot manifests are generated around the real chunk digests — a manifest
// md5 that does not match the served bytes would fail the transfer's hash
// check, which is exactly the property this test leans on.
func TestInstallEndToEnd(t *testing.T) {
	f := newPlanFixture(t)

	base := planChunkPayload(t, "base data content")
	dlc := planChunkPayload(t, "dlc data content")
	dep := planChunkPayload(t, "dependency content")
	sfc := planChunkPayload(t, "sfc container content")

	v2New := galaxy.HashToGalaxyPath(planBuildHashNew)
	v2Old := galaxy.HashToGalaxyPath(planBuildHashOld)

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

	// The base depot: the regular file, the container and the file inside it.
	// The container chunk's digest is the container file's own.
	f.set("/content-system/v2/meta/"+galaxy.HashToGalaxyPath(planDepotHashLang),
		`{"depot":{`+
			`"smallFilesContainer":{"chunks":[{"compressedMd5":"`+sfc.md5+`","size":100}]},`+
			`"items":[`+
			`{"path":"game/data.bin","md5":"base-item-md5","chunks":[{"compressedMd5":"`+base.md5+`","size":100}]},`+
			`{"path":"game/small1.txt","sfcRef":{"offset":0,"size":100},"chunks":[{"compressedMd5":"`+sfc.md5+`","size":100}]}]}}`)

	// The DLC depot re-declares game/data.bin with different content: the dedup
	// must install the DLC's bytes over the base game's.
	f.set("/content-system/v2/meta/"+galaxy.HashToGalaxyPath(planDepotHashDLC),
		`{"depot":{"items":[`+
			`{"path":"game/data.bin","md5":"dlc-item-md5","chunks":[{"compressedMd5":"`+dlc.md5+`","size":100}]}]}}`)

	f.set("/content-system/v2/meta/"+v2Old,
		`{"baseProductId":"`+planProductID+`","installDirectory":"W3 GOTY","version":2,`+
			`"depots":[{"productId":"`+planProductID+`","languages":["en-US"],"osBitness":["64"],"manifest":"`+planOldDepotHash+`"}]}`)
	f.set("/content-system/v2/meta/"+galaxy.HashToGalaxyPath(planOldDepotHash),
		`{"depot":{"items":[`+
			`{"path":"game/data.bin","md5":"base-item-md5","chunks":[{"compressedMd5":"`+base.md5+`","size":100}]},`+
			`{"path":"game/oldfile.bin","md5":"old-item-md5","chunks":[{"compressedMd5":"`+base.md5+`","size":100}]}]}}`)

	f.set("/dependencies/repository",
		`{"repository_manifest":"https://content-system.gog.com/dep/repo-manifest"}`)
	f.set("/dep/repo-manifest",
		`{"depots":[{"dependencyId":"dep1","productId":"`+planDepProductID+`",`+
			`"languages":["en-US"],"osBitness":["64"],"manifest":"`+planDepotHashDep+`"}]}`)
	f.set("/content-system/v2/dependencies/meta/"+galaxy.HashToGalaxyPath(planDepotHashDep),
		`{"depot":{"items":[`+
			`{"path":"game/dep/depfile.bin","md5":"dep-item-md5","chunks":[{"compressedMd5":"`+dep.md5+`","size":100}]}]}}`)

	// The chunk bytes the templates resolve to, keyed by the galaxy path the
	// URL carries (HashToGalaxyPath of the chunk digest).
	for _, p := range []chunkPayload{base, dlc, dep, sfc} {
		f.set("/chunks/"+galaxy.HashToGalaxyPath(p.md5), p.compressed)
	}

	cfg := planTestConfig(t)
	// The CDN priority must put the rewritten host first: the transport only
	// maps cdn.gog.com into the test server.
	cfg.DownloadConfig.GalaxyCDNPriority = []string{"cdnMain", "cdnAlt"}
	if err := os.MkdirAll(cfg.ConfigDirectory, 0o755); err != nil {
		t.Fatal(err)
	}

	// The link documents the URL provider reads during the transfer: regular
	// chunks come from secure_link with an empty path parameter, the
	// dependency's chunk path arrives on the open_link response.
	depGalaxyPath := galaxy.HashToGalaxyPath(dep.md5)
	// Each product's id gets its own secure_link call; the body is shared
	// because the template does not depend on it.
	secureLinks := `{"urls":[{"endpoint_name":"cdnAlt","url_format":"https://alt.gog.com/chunks{path}","parameters":{"path":""}},` +
		`{"endpoint_name":"cdnMain","url_format":"https://cdn.gog.com/chunks{path}","parameters":{"path":""}}]}`
	f.set("/products/"+planProductID+"/secure_link", secureLinks)
	f.set("/products/"+planDLCProductID+"/secure_link", secureLinks)
	f.set("/open_link",
		`{"urls":[{"endpoint_name":"cdnMain","url_format":"https://cdn.gog.com/chunks{path}",`+
			`"parameters":{"path":"/`+depGalaxyPath+`"}}]}`)

	// A previously installed build with a file the new build dropped: it must
	// be deleted during ApplyPlanChanges. The file inside the container is
	// pre-created on purpose — it is NOT installed by this run, so the SFC
	// decision must keep it out of the tasks while leaving it un-deleted.
	installPath := cfg.Directories.Directory + "W3 GOTY"
	if err := os.MkdirAll(installPath+"/game", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installPath+"/goggame-"+planProductID+".info",
		[]byte(`{"buildId":"b-old"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installPath+"/game/oldfile.bin", []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	console := newFakeConsole()
	d := newOfflineDownloader(t, f.Server, cfg, console)
	// The offline downloader builds an empty credential store; give it a fresh
	// token so the transfer's per-chunk expiry checks pass without a refresh.
	d.token.SetJSON(map[string]any{
		"access_token": "at", "refresh_token": "rt", "expires_in": 3600, "user_id": "u1",
	})
	if err := d.Install(context.Background(), NewInstallRequest(cfg, planProductID, "")); err != nil {
		t.Fatalf("Install: %v", err)
	}

	// The assembled files carry the decompressed content of the chunks the
	// plan selected — the DLC's bytes over the base game's for data.bin — and
	// the small-files container unpacks its member and is gone.
	assertFileContent(t, installPath+"/game/data.bin", dlc.content)
	assertFileContent(t, installPath+"/game/dep/depfile.bin", dep.content)
	// The fixture's sfcRef advertises 100 bytes but the container holds fewer:
	// the section reader stops at EOF, so the member carries the whole
	// container body.
	assertFileContent(t, installPath+"/game/small1.txt", sfc.content)
	assertFileAbsent(t, installPath+"/galaxy_smallfilescontainer_"+planProductID)

	// The gone file was removed by ApplyPlanChanges.
	assertFileAbsent(t, installPath+"/game/oldfile.bin")

	out := console.out.String()
	for _, want := range []string{
		"The Witcher 3: Wild Hunt", "Files: 3",
		"Extracting small files container " + installPath + "/galaxy_smallfilescontainer_" + planProductID,
		"Deleting small files container " + installPath + "/galaxy_smallfilescontainer_" + planProductID,
		"Checking for orphaned files",
		// The install metadata file is an orphan as written; there is no
		// special case for it. Deletion is off, so it survives.
		"\t1 orphaned files",
	} {
		if !bytes.Contains([]byte(out), []byte(want)) {
			t.Errorf("console output missing %q", want)
		}
	}
	assertFileContent(t, installPath+"/goggame-"+planProductID+".info", `{"buildId":"b-old"}`)
}

// chunkGate is the network exit this test hands the run: every request goes
// through to the fixture, and a chunk body is handed over in two steps. The
// transfer's first read of the body is answered with whatever the fixture
// flushed; the second read stops here — after that first read's bytes have been
// published into the progress registry, and before any of the rest can arrive.
// That is what makes the sample below an observation rather than a poll.
type chunkGate struct {
	inner   http.RoundTripper
	arrived chan struct{}
	release <-chan struct{}
	once    sync.Once
}

func (g *chunkGate) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := g.inner.RoundTrip(req)
	if err != nil || !strings.Contains(req.URL.Path, "/chunks/") {
		return resp, err
	}
	resp.Body = &gatedBody{body: resp.Body, gate: g}
	return resp, nil
}

// gatedBody stops the read after the first one: closing arrived tells the test
// the first read's bytes are published, and the read returns once the test
// releases the run.
type gatedBody struct {
	body  io.ReadCloser
	gate  *chunkGate
	reads int
}

func (b *gatedBody) Read(p []byte) (int, error) {
	if b.reads > 0 {
		b.gate.once.Do(func() { close(b.gate.arrived) })
		<-b.gate.release
	}
	b.reads++
	return b.body.Read(p)
}

func (b *gatedBody) Close() error { return b.body.Close() }

// TestInstallPublishesProgressThroughTheRun locks the injection chain the front
// end relies on: the registry handed in through Dependencies reaches the
// transfer run, which publishes into it while a chunk is still arriving and
// clears the slot once the task is done. The fixture holds the second half of
// the only chunk until the test has looked, and the transport holds the read
// that would consume it, so the partial reading is a controlled moment rather
// than a race.
func TestInstallPublishesProgressThroughTheRun(t *testing.T) {
	f := newPlanFixture(t)
	payload := planChunkPayload(t, "progress through the install run")
	v2New := galaxy.HashToGalaxyPath(planBuildHashNew)

	f.set("/products/"+planProductID+"/os/windows/builds",
		`{"items":[{"build_id":"b-new","version_name":"1.0.2","date_published":"2024-03-02","generation":2,`+
			`"link":"https://cdn.gog.com/content-system/v2/meta/`+v2New+`"}]}`)
	f.set("/content-system/v2/meta/"+v2New,
		`{"baseProductId":"`+planProductID+`","installDirectory":"W3 GOTY","version":2,`+
			`"products":[{"name":"The Witcher 3: Wild Hunt"}],`+
			`"depots":[{"productId":"`+planProductID+`","languages":["en-US"],"osBitness":["64"],`+
			`"manifest":"`+planDepotHashLang+`"}]}`)
	// The chunk declares its real compressed size, so the task's logical total
	// is the size the registry must report as Total.
	f.set("/content-system/v2/meta/"+galaxy.HashToGalaxyPath(planDepotHashLang),
		`{"depot":{"items":[{"path":"game/data.bin","md5":"base-item-md5","chunks":[`+
			`{"compressedMd5":"`+payload.md5+`","compressedSize":`+fmt.Sprint(len(payload.compressed))+`,`+
			`"size":`+fmt.Sprint(len(payload.content))+`}]}]}}`)
	f.set("/products/"+planProductID+"/secure_link",
		`{"urls":[{"endpoint_name":"cdnMain","url_format":"https://cdn.gog.com/chunks{path}",`+
			`"parameters":{"path":""}}]}`)

	release := make(chan struct{})
	var once sync.Once
	f.hold("/chunks/"+galaxy.HashToGalaxyPath(payload.md5), payload.compressed, release)
	defer once.Do(func() { close(release) })

	cfg := planTestConfig(t)
	cfg.DownloadConfig.GalaxyCDNPriority = []string{"cdnMain"}
	if err := os.MkdirAll(cfg.ConfigDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	destination := cfg.Directories.Directory + "W3 GOTY/game/data.bin"
	progress := transfer.NewProgress()

	target, err := url.Parse(f.Server.URL)
	if err != nil {
		t.Fatal(err)
	}
	gate := &chunkGate{
		inner:   &gogHostTransport{target: target},
		arrived: make(chan struct{}),
		release: release,
	}

	d := newOfflineDownloaderWith(t, f.Server, cfg, newFakeConsole(),
		Dependencies{Progress: progress, HTTPTransport: gate})
	// The offline downloader builds an empty credential store; give it a fresh
	// token so the transfer's per-chunk expiry checks pass without a refresh.
	d.token.SetJSON(map[string]any{
		"access_token": "at", "refresh_token": "rt", "expires_in": 3600, "user_id": "u1",
	})

	done := make(chan error, 1)
	go func() { done <- d.Install(context.Background(), NewInstallRequest(cfg, planProductID, "")) }()

	select {
	case <-gate.arrived:
	case err := <-done:
		t.Fatalf("Install finished before the chunk body was gated (err = %v)", err)
	}
	if v, ok := progress.Bytes(destination); !ok || v <= 0 || v >= int64(len(payload.compressed)) {
		t.Fatalf("in-flight sample for %s: Bytes = (%d, %v), want a partial count below %d",
			destination, v, ok, len(payload.compressed))
	}
	if v, ok := progress.Total(destination); !ok || v != int64(len(payload.compressed)) {
		t.Errorf("Total while in flight = (%d, %v), want (%d, true)", v, ok, len(payload.compressed))
	}

	once.Do(func() { close(release) })
	if err := <-done; err != nil {
		t.Fatalf("Install: %v", err)
	}
	if _, ok := progress.Bytes(destination); ok {
		t.Error("slot survived the finished install")
	}
	if _, ok := progress.Total(destination); ok {
		t.Error("total survived the finished install")
	}
	assertFileContent(t, destination, payload.content)
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(data) != want {
		t.Errorf("%s = %q, want %q", path, data, want)
	}
}

func assertFileAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err == nil {
		t.Errorf("%s exists, want it gone", path)
	}
}

// TestInstallRequestCarriesContext locks that Install surfaces a cancelled
// context instead of reporting a finished install.
func TestInstallRequestCarriesContext(t *testing.T) {
	f := newPlanFixture(t)
	f.setDefaultBodies()
	cfg := planTestConfig(t)
	d := newOfflineDownloader(t, f.Server, cfg, newFakeConsole())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := NewInstallRequest(cfg, planProductID, "")
	if err := d.Install(ctx, req); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want the context cancellation", err)
	}
}

// TestRevalidateSkipped locks the install-level gate over the plan's skipped
// set: a destination the plan observed as
// complete is only a planning-time observation. The helper must pass while
// the file still matches, fail with the destination and the rerun guidance
// once the file changed after that observation, and surface an observation
// failure as an inspection error — never as a mismatch.
func TestRevalidateSkipped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.bin")
	content := []byte("observed complete at plan time")
	sum := md5.Sum(content)
	item := model.GalaxyDepotItem{
		Path:      "game/a.bin",
		TotalSize: uint64(len(content)),
		MD5:       hex.EncodeToString(sum[:]),
	}

	// The original observation: the file matches, so the plan skipped it.
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	skipped := []SkippedFile{{Destination: path, Item: item}}
	if err := revalidateSkipped(skipped); err != nil {
		t.Fatalf("revalidateSkipped = %v, want nil while the file still matches", err)
	}

	// The race the revalidation closes: the file changes after the plan
	// observed it.
	if err := os.WriteFile(path, []byte("externally modified"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := revalidateSkipped(skipped)
	if err == nil {
		t.Fatal("revalidateSkipped = nil, want the changed-file failure")
	}
	if !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "changed during installation") {
		t.Errorf("err = %v, want the destination and the rerun guidance", err)
	}

	// An observation failure is an installation error, never folded into a
	// mismatch: a NUL path makes the stat fail on every platform.
	broken := []SkippedFile{{Destination: "bad\x00path", Item: item}}
	if err := revalidateSkipped(broken); err == nil {
		t.Fatal("revalidateSkipped = nil, want the inspection error")
	} else if !strings.Contains(err.Error(), "Failed to inspect") {
		t.Errorf("err = %v, want the inspection error", err)
	}
}
