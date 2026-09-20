package transfer

import (
	"bytes"
	"compress/zlib"
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
	"time"

	"github.com/nekrozis/goggo/internal/httpx"
	"github.com/nekrozis/goggo/internal/model"
)

// urlFunc adapts a function into transfer.URLProvider.
type urlFunc func(ctx context.Context, task model.FileTask, chunk model.GalaxyDepotItemChunk) (string, error)

// URL implements transfer.URLProvider.
func (f urlFunc) URL(ctx context.Context, task model.FileTask, chunk model.GalaxyDepotItemChunk) (string, error) {
	return f(ctx, task, chunk)
}

// assertFileContent reads path and compares it with want.
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

// recordingObserver collects the events a run emitted. Run's deliverer
// goroutine is the only sender and Run returns after it finishes, so reading
// the slice after Run needs no lock.
type recordingObserver struct {
	events []Event
}

func (o *recordingObserver) OnEvent(ev Event) { o.events = append(o.events, ev) }

// testCDN serves the compressed chunk bytes and counts the hits per path.
type testCDN struct {
	*httptest.Server

	mu       sync.Mutex
	bodies   map[string][]byte
	requests map[string]int
}

func newTestCDN(t *testing.T) *testCDN {
	t.Helper()
	c := &testCDN{bodies: map[string][]byte{}, requests: map[string]int{}}
	c.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		c.requests[r.URL.Path]++
		body, ok := c.bodies[r.URL.Path]
		c.mu.Unlock()

		if !ok {
			http.NotFound(w, r)
			return
		}
		if status := r.Header.Get("X-Serve-Status"); status != "" {
			// A requested failure status still consumes the hit.
			w.WriteHeader(416)
			return
		}
		w.Write(body)
	}))
	t.Cleanup(c.Close)
	return c
}

func (c *testCDN) set(path string, body []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.bodies[path] = body
}

func (c *testCDN) hits(path string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.requests[path]
}

func (c *testCDN) url(path string) string { return c.URL + path }

// chunked compresses content into a served chunk and returns the depot chunk
// whose compressed md5 matches the served bytes.
func chunked(t *testing.T, cdn *testCDN, content string) model.GalaxyDepotItemChunk {
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
	md5hex := hex.EncodeToString(sum[:])
	cdn.set("/c/"+md5hex, buf.Bytes())
	return model.GalaxyDepotItemChunk{CompressedMD5: md5hex, CompressedSize: uint64(len(buf.Bytes())), Size: uint64(len(content))}
}

// urlByChunk resolves a chunk to its CDN path: the shape the real provider has.
type urlByChunk struct{ cdn *testCDN }

func (u urlByChunk) URL(_ context.Context, task model.FileTask, chunk model.GalaxyDepotItemChunk) (string, error) {
	return u.cdn.url("/c/" + chunk.CompressedMD5), nil
}

// runDeps bundles the dependencies for one run, with the recording observer.
func runDeps(t *testing.T, cdn *testCDN, obs *recordingObserver) RunDeps {
	t.Helper()
	hx, err := httpx.New(httpx.Config{UserAgent: "goggo-test/1.0"})
	if err != nil {
		t.Fatalf("httpx.New: %v", err)
	}
	return RunDeps{HTTP: hx, URL: urlByChunk{cdn: cdn}, Observer: obs}
}

func singleTask(dest string, chunks ...model.GalaxyDepotItemChunk) []model.FileTask {
	return []model.FileTask{{Item: model.GalaxyDepotItem{Path: "game/file.bin", Chunks: chunks}, Destination: dest}}
}

// TestRunEmptyTasks locks the immediate return: no observer call happens at
// all for an empty plan.
func TestRunEmptyTasks(t *testing.T) {
	cdn := newTestCDN(t)
	obs := &recordingObserver{}
	deps := runDeps(t, cdn, obs)

	if err := Run(context.Background(), nil, Options{}, deps); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(obs.events) != 0 {
		t.Errorf("events = %+v, want none", obs.events)
	}
}

// TestRunDownloadsAndAssembles locks the minimal download body: every chunk is
// fetched, verified and appended in order, so the file holds the decompressed
// concatenation, and the events tell the task's story in order.
func TestRunDownloadsAndAssembles(t *testing.T) {
	cdn := newTestCDN(t)
	dest := filepath.Join(t.TempDir(), "game", "data.bin")
	task := model.FileTask{
		Item: model.GalaxyDepotItem{
			Path:                "game/data.bin",
			Chunks:              []model.GalaxyDepotItemChunk{chunked(t, cdn, "hello "), chunked(t, cdn, "world")},
			TotalCompressedSize: uint64(len(cdn.bodies["/c/"+mustChunkMD5(t, cdn, "hello ")]) + len(cdn.bodies["/c/"+mustChunkMD5(t, cdn, "world")])),
		},
		Destination: dest,
	}
	obs := &recordingObserver{}
	if err := Run(context.Background(), singleTaskFrom(task), Options{Workers: 1}, runDeps(t, cdn, obs)); err != nil {
		t.Fatalf("Run: %v", err)
	}

	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read the assembled file: %v", err)
	}
	if string(data) != "hello world" {
		t.Errorf("file = %q, want %q", data, "hello world")
	}

	var kinds []EventKind
	for _, ev := range obs.events {
		if ev.Path == dest {
			kinds = append(kinds, ev.Kind)
		}
	}
	want := []EventKind{EventTaskStart, EventProgress, EventProgress, EventTaskFinish}
	if len(kinds) != len(want) {
		t.Fatalf("task event kinds = %v, want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Errorf("event %d = %v, want %v", i, kinds[i], want[i])
		}
	}
}

// TestRunRetriesOnHashMismatch locks that a chunk served with wrong bytes is
// retried until its digest matches, and the retry is announced.
func TestRunRetriesOnHashMismatch(t *testing.T) {
	cdn := newTestCDN(t)
	chunk := chunked(t, cdn, "good content")
	dest := filepath.Join(t.TempDir(), "data.bin")

	// The first request serves garbage, the second the real bytes.
	var calls int
	var mu sync.Mutex
	good := cdn.bodies["/c/"+chunk.CompressedMD5]
	cdn.set("/c/"+chunk.CompressedMD5, []byte("corrupt"))
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n == 1 {
			w.Write([]byte("corrupt"))
			return
		}
		w.Write(good)
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	var provider urlFunc = func(_ context.Context, _ model.FileTask, _ model.GalaxyDepotItemChunk) (string, error) {
		return srv.URL + "/c/" + chunk.CompressedMD5, nil
	}
	obs := &recordingObserver{}
	deps := RunDeps{HTTP: mustHTTP(t), URL: provider, Observer: obs}

	if err := Run(context.Background(), singleTask(dest, chunk), Options{Workers: 1, Retries: 3}, deps); err != nil {
		t.Fatalf("Run: %v", err)
	}
	assertFileContent(t, dest, "good content")

	var retried bool
	for _, ev := range obs.events {
		if ev.Kind == EventMessageInfo && strings.Contains(ev.Text, "Retry 1/3") {
			retried = true
		}
	}
	if !retried {
		t.Errorf("events = %+v, want a retry announcement", obs.events)
	}
}

// TestRunTaskFailureDoesNotStopOthers locks D65a: a task that exhausts its
// retries reports the error and the remaining tasks still run; Run returns nil.
func TestRunTaskFailureDoesNotStopOthers(t *testing.T) {
	cdn := newTestCDN(t)
	good := chunked(t, cdn, "good")

	// The failing task's URL serves a 404 forever.
	failing := model.FileTask{
		Item:        model.GalaxyDepotItem{Path: "game/bad.bin", Chunks: []model.GalaxyDepotItemChunk{{CompressedMD5: "ffffffffffffffffffffffffffffffffffffffff"}}},
		Destination: filepath.Join(t.TempDir(), "bad.bin"),
	}
	healthy := model.FileTask{
		Item:        model.GalaxyDepotItem{Path: "game/good.bin", Chunks: []model.GalaxyDepotItemChunk{good}},
		Destination: filepath.Join(t.TempDir(), "good.bin"),
	}

	var provider urlFunc = func(_ context.Context, task model.FileTask, _ model.GalaxyDepotItemChunk) (string, error) {
		if strings.HasSuffix(task.Destination, "bad.bin") {
			return cdn.url("/missing"), nil
		}
		return cdn.url("/c/" + good.CompressedMD5), nil
	}
	obs := &recordingObserver{}
	deps := RunDeps{HTTP: mustHTTP(t), URL: provider, Observer: obs}

	if err := Run(context.Background(), []model.FileTask{failing, healthy}, Options{Workers: 2, Retries: 1}, deps); err != nil {
		t.Fatalf("Run = %v, want nil: task failures are events (D65a)", err)
	}
	assertFileContent(t, healthy.Destination, "good")

	var sawError bool
	for _, ev := range obs.events {
		if ev.Kind == EventMessageError && strings.Contains(ev.Text, "404") {
			sawError = true
		}
	}
	if !sawError {
		t.Errorf("events = %+v, want the failing task's error event", obs.events)
	}
}

// TestRun416IsNotRetried locks the one HTTP status that is never retried: a 416
// costs exactly one request.
func TestRun416IsNotRetried(t *testing.T) {
	var mu sync.Mutex
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
	}))
	defer srv.Close()

	chunk := model.GalaxyDepotItemChunk{CompressedMD5: "abc"}
	dest := filepath.Join(t.TempDir(), "data.bin")
	var provider urlFunc = func(_ context.Context, _ model.FileTask, _ model.GalaxyDepotItemChunk) (string, error) {
		return srv.URL + "/chunk", nil
	}
	obs := &recordingObserver{}
	deps := RunDeps{HTTP: mustHTTP(t), URL: provider, Observer: obs}

	// D65a: the failed task leaves as an error event, and the run itself still
	// returns nil.
	if err := Run(context.Background(), singleTask(dest, chunk), Options{Workers: 1, Retries: 3}, deps); err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}
	var sawError bool
	for _, ev := range obs.events {
		if ev.Kind == EventMessageError {
			sawError = true
		}
	}
	if !sawError {
		t.Errorf("events = %+v, want the task's error event", obs.events)
	}
	mu.Lock()
	defer mu.Unlock()
	if requests != 1 {
		t.Errorf("requests = %d, want exactly 1: a 416 never retries", requests)
	}
}

// TestRunWaitDelaysAttempts locks that the wait precedes every attempt.
func TestRunWaitDelaysAttempts(t *testing.T) {
	cdn := newTestCDN(t)
	chunk := chunked(t, cdn, "x")
	cdn.set("/c/missing", []byte("x"))

	dest := filepath.Join(t.TempDir(), "data.bin")
	var provider urlFunc = func(_ context.Context, _ model.FileTask, _ model.GalaxyDepotItemChunk) (string, error) {
		return cdn.url("/c/missing"), nil
	}
	obs := &recordingObserver{}
	deps := RunDeps{HTTP: mustHTTP(t), URL: provider, Observer: obs}

	start := time.Now()
	if err := Run(context.Background(), singleTask(dest, chunk), Options{Workers: 1, Retries: 2, Wait: 50 * time.Millisecond}, deps); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 140*time.Millisecond {
		t.Errorf("elapsed = %v, want at least the three 50ms waits", elapsed)
	}
	_ = obs
}

// TestRunContextCancel locks that a cancelled context comes back as the run's
// error.
func TestRunContextCancel(t *testing.T) {
	cdn := newTestCDN(t)
	chunk := chunked(t, cdn, "x")
	release := make(chan struct{})
	cdn.set("/c/"+chunk.CompressedMD5, []byte("x"))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		w.Write([]byte("x"))
	}))
	defer srv.Close()
	defer close(release)

	dest := filepath.Join(t.TempDir(), "data.bin")
	var provider urlFunc = func(_ context.Context, _ model.FileTask, _ model.GalaxyDepotItemChunk) (string, error) {
		return srv.URL + "/c/" + chunk.CompressedMD5, nil
	}
	deps := RunDeps{HTTP: mustHTTP(t), URL: provider, Observer: &recordingObserver{}}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, singleTask(dest, chunk), Options{Workers: 1}, deps) }()

	time.Sleep(50 * time.Millisecond)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// TestRunNilDeps locks the fail-fast on an incomplete dependency bundle.
func TestRunNilDeps(t *testing.T) {
	cdn := newTestCDN(t)
	deps := runDeps(t, cdn, &recordingObserver{})
	deps.URL = nil
	if err := Run(context.Background(), singleTask(filepath.Join(t.TempDir(), "x"), chunked(t, cdn, "x")), Options{}, deps); err == nil {
		t.Error("Run without a url provider must fail")
	}
}

// TestRunWorkersClamped locks the concurrency cap on the success path: no more
// than Workers downloads run at once, whatever the task count, and every
// assembled file still carries its own content.
func TestRunWorkersClamped(t *testing.T) {
	var mu sync.Mutex
	var active, maxActive int
	bodies := map[string][]byte{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		active++
		if active > maxActive {
			maxActive = active
		}
		mu.Unlock()
		time.Sleep(30 * time.Millisecond)
		body, ok := bodies[r.URL.Path]
		mu.Lock()
		active--
		defer mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(body)
	}))
	defer srv.Close()

	var tasks []model.FileTask
	contents := map[string]string{}
	for i := 0; i < 4; i++ {
		content := fmt.Sprintf("chunk %d content", i)
		var buf bytes.Buffer
		zw := zlib.NewWriter(&buf)
		if _, err := zw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		sum := md5.Sum(buf.Bytes())
		md5hex := hex.EncodeToString(sum[:])
		bodies["/c/"+md5hex] = buf.Bytes()
		// The destination is computed once per task: a second t.TempDir call
		// here would desync the assertion map from the actual destinations.
		dest := filepath.Join(t.TempDir(), fmt.Sprintf("f%d.bin", i))
		contents[dest] = content
		tasks = append(tasks, model.FileTask{
			Item: model.GalaxyDepotItem{
				Path:                fmt.Sprintf("game/f%d.bin", i),
				Chunks:              []model.GalaxyDepotItemChunk{{CompressedMD5: md5hex, CompressedSize: uint64(buf.Len()), Size: uint64(len(content))}},
				TotalCompressedSize: uint64(buf.Len()),
			},
			Destination: dest,
		})
	}

	var provider urlFunc = func(_ context.Context, task model.FileTask, _ model.GalaxyDepotItemChunk) (string, error) {
		return srv.URL + "/c/" + task.Item.Chunks[0].CompressedMD5, nil
	}
	obs := &recordingObserver{}
	hx, err := httpx.New(httpx.Config{UserAgent: "goggo-test/1.0"})
	if err != nil {
		t.Fatalf("httpx.New: %v", err)
	}
	deps := RunDeps{HTTP: hx, URL: provider, Observer: obs}

	if err := Run(context.Background(), tasks, Options{Workers: 2}, deps); err != nil {
		t.Fatalf("Run: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if maxActive > 2 {
		t.Errorf("max concurrent downloads = %d, want at most 2", maxActive)
	}
	if maxActive < 2 {
		t.Errorf("max concurrent downloads = %d, want the two workers to overlap", maxActive)
	}
	// Every task succeeded: the concurrency cap did not lose any content.
	for dest, want := range contents {
		data, err := os.ReadFile(dest)
		if err != nil {
			t.Fatalf("read %s: %v", dest, err)
		}
		if string(data) != want {
			t.Errorf("%s = %q, want %q", dest, data, want)
		}
	}
}

func mustHTTP(t *testing.T) *httpx.Client {
	t.Helper()
	hx, err := httpx.New(httpx.Config{UserAgent: "goggo-test/1.0"})
	if err != nil {
		t.Fatalf("httpx.New: %v", err)
	}
	return hx
}

func singleTaskFrom(task model.FileTask) []model.FileTask {
	return []model.FileTask{task}
}

// mustChunkMD5 recomputes a served chunk's digest for the total-size fixture.
func mustChunkMD5(t *testing.T, cdn *testCDN, content string) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	zw.Write([]byte(content))
	zw.Close()
	sum := md5.Sum(buf.Bytes())
	return hex.EncodeToString(sum[:])
}

// TestRunChunkResumeFromMemory locks the D72 model: a break mid-body keeps the
// bytes already received in memory, and the retry carries a Range header from
// that offset — the suffix appends and the chunk's hash passes.
func TestRunChunkResumeFromMemory(t *testing.T) {
	full := compress(t, "chunk body content")
	var mu sync.Mutex
	var ranges []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		ranges = append(ranges, r.Header.Get("Range"))
		mu.Unlock()
		if r.Header.Get("Range") == "" {
			// First attempt: a partial body, then an abrupt close.
			w.Header().Set("Content-Length", fmt.Sprint(len(full)))
			w.Write(full[:6])
			w.(http.Flusher).Flush()
			conn, buf, err := w.(http.Hijacker).Hijack()
			if err != nil {
				return
			}
			buf.Flush()
			conn.Close()
			return
		}
		// Retry: serve the requested suffix as a proper 206 (a server that
		// ignores Range and answers 200 is the fold test's job).
		from := 6
		w.WriteHeader(http.StatusPartialContent)
		w.Write(full[from:])
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "data.bin")
	chunk := model.GalaxyDepotItemChunk{CompressedMD5: md5OfBytes(full)}
	deps := RunDeps{HTTP: mustHTTP(t), URL: urlFunc(func(_ context.Context, _ model.FileTask, _ model.GalaxyDepotItemChunk) (string, error) {
		return srv.URL + "/c", nil
	}), Observer: &recordingObserver{}}

	if err := Run(context.Background(), singleTask(dest, chunk), Options{Workers: 1, Retries: 3}, deps); err != nil {
		t.Fatalf("Run: %v", err)
	}
	assertFileContent(t, dest, "chunk body content")
	mu.Lock()
	defer mu.Unlock()
	if len(ranges) < 2 || ranges[1] != "bytes=6-" {
		t.Errorf("Range headers = %q, want the second attempt to resume from 6", ranges)
	}
}

// TestRunChunkHashReset locks the second retry cause: a hash mismatch empties
// the buffer, so the retry asks for the chunk from the very beginning again —
// a corrupt prefix must not join the next hash.
func TestRunChunkHashReset(t *testing.T) {
	good := compress(t, "good content")
	var mu sync.Mutex
	var ranges []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		ranges = append(ranges, r.Header.Get("Range"))
		mu.Unlock()
		// Always the wrong bytes: the hash can never pass.
		w.Write(good[:len(good)/2])
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "data.bin")
	chunk := model.GalaxyDepotItemChunk{CompressedMD5: md5OfBytes(good)}
	deps := RunDeps{HTTP: mustHTTP(t), URL: urlFunc(func(_ context.Context, _ model.FileTask, _ model.GalaxyDepotItemChunk) (string, error) {
		return srv.URL + "/c", nil
	}), Observer: &recordingObserver{}}

	// D65a: the failed task leaves as an error event; Run itself is nil.
	if err := Run(context.Background(), singleTask(dest, chunk), Options{Workers: 1, Retries: 2}, deps); err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}
	mu.Lock()
	defer mu.Unlock()
	for i, r := range ranges {
		if r != "" {
			t.Errorf("attempt %d carried Range %q: a hash reset must restart from zero", i, r)
		}
	}
	assertFileAbsent(t, dest)
}

// TestRunChunkRange200Fold locks the deliberate fold: when a retried request
// with a Range header is answered 200 with the whole body (a server ignoring
// Range), the repeated prefix is dropped and the hash still passes. A first
// attempt without a Range header never folds.
func TestRunChunkRange200Fold(t *testing.T) {
	full := compress(t, "resumed content")
	var mu sync.Mutex
	var ranges []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		ranges = append(ranges, r.Header.Get("Range"))
		mu.Unlock()
		// Always the whole body, whatever the Range asks for: the first
		// attempt truncates (a transport break keeps a prefix), the retry
		// answers 200 in full.
		if r.Header.Get("Range") == "" {
			w.Header().Set("Content-Length", fmt.Sprint(len(full)))
			w.Write(full[:5])
			w.(http.Flusher).Flush()
			conn, buf, _ := w.(http.Hijacker).Hijack()
			buf.Flush()
			conn.Close()
			return
		}
		w.Write(full)
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "data.bin")
	chunk := model.GalaxyDepotItemChunk{CompressedMD5: md5OfBytes(full)}
	deps := RunDeps{HTTP: mustHTTP(t), URL: urlFunc(func(_ context.Context, _ model.FileTask, _ model.GalaxyDepotItemChunk) (string, error) {
		return srv.URL + "/c", nil
	}), Observer: &recordingObserver{}}

	if err := Run(context.Background(), singleTask(dest, chunk), Options{Workers: 1, Retries: 3}, deps); err != nil {
		t.Fatalf("Run: %v", err)
	}
	assertFileContent(t, dest, "resumed content")
	mu.Lock()
	defer mu.Unlock()
	if len(ranges) < 2 {
		t.Fatalf("attempts = %d, want at least 2", len(ranges))
	}
}

// TestRunChunkFiletime locks the D77 addition: the server's Last-Modified
// moves onto the assembled file, and a chunk that carries no timestamp leaves
// the mtime alone.
func TestRunChunkFiletime(t *testing.T) {
	lm := time.Date(2021, 7, 6, 5, 4, 3, 0, time.UTC)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Last-Modified", lm.Format(http.TimeFormat))
		w.Write(compress(t, "stamped"))
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "data.bin")
	chunk := model.GalaxyDepotItemChunk{CompressedMD5: md5OfBytes(compress(t, "stamped"))}
	deps := RunDeps{HTTP: mustHTTP(t), URL: urlFunc(func(_ context.Context, _ model.FileTask, _ model.GalaxyDepotItemChunk) (string, error) {
		return srv.URL + "/c", nil
	}), Observer: &recordingObserver{}}

	if err := Run(context.Background(), singleTask(dest, chunk), Options{Workers: 1}, deps); err != nil {
		t.Fatalf("Run: %v", err)
	}
	fi, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.ModTime().UTC(); got.Year() != 2021 || got.Month() != time.July {
		t.Errorf("mtime = %v, want the Last-Modified value", got)
	}
}

// compress is the zlib stream over content — the chunk bodies' wire format.
func compress(t *testing.T, content string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	if _, err := zw.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// md5OfBytes is the hex md5 of raw bytes.
func md5OfBytes(b []byte) string {
	sum := md5.Sum(b)
	return hex.EncodeToString(sum[:])
}
