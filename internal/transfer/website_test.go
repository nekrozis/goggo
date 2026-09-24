package transfer

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nekrozis/goggo/internal/httpx"
	"github.com/nekrozis/goggo/internal/manifest/gogxml"
	"github.com/nekrozis/goggo/internal/model"
)

// resolveResult is one website task's configured resolution.
type resolveResult struct {
	downlink    string
	checksumXML string
	err         error
}

// fakeWebsiteURL resolves by the task's base name, so each test wires its own
// responses per file.
type fakeWebsiteURL struct {
	mu  sync.Mutex
	res map[string]resolveResult
}

func (w *fakeWebsiteURL) set(base string, r resolveResult) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.res[base] = r
}

func (w *fakeWebsiteURL) Resolve(_ context.Context, task model.WebsiteTask) (string, string, error) {
	w.mu.Lock()
	r, ok := w.res[filepath.Base(task.Destination)]
	w.mu.Unlock()
	if !ok {
		return "", "", fmt.Errorf("no resolve result for %s", task.Destination)
	}
	return r.downlink, r.checksumXML, r.err
}

// websiteFixture serves the downlink and checksum documents and the file bytes,
// and records the hits and headers per path.
type websiteFixture struct {
	*httptest.Server

	mu      sync.Mutex
	bodies  map[string]string
	hits    map[string]int
	headers map[string]http.Header
}

func newWebsiteFixture(t *testing.T) *websiteFixture {
	t.Helper()
	f := &websiteFixture{
		bodies:  map[string]string{},
		hits:    map[string]int{},
		headers: map[string]http.Header{},
	}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.hits[r.URL.Path]++
		f.headers[r.URL.Path] = r.Header.Clone()
		body, ok := f.bodies[r.URL.Path]
		f.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		if rangeHdr := r.Header.Get("Range"); strings.HasPrefix(rangeHdr, "bytes=") && strings.HasSuffix(rangeHdr, "-") {
			// The header is "bytes=<from>-": the trailing dash is part of the
			// open-ended form and must go before the number is parsed.
			fromStr := strings.TrimSuffix(strings.TrimPrefix(rangeHdr, "bytes="), "-")
			if from, err := strconv.Atoi(fromStr); err == nil && from >= 0 && from < len(body) {
				body = body[from:]
			} else if from >= len(body) {
				w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
				return
			}
		}
		w.Write([]byte(body))
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *websiteFixture) set(path, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bodies[path] = body
}

func (f *websiteFixture) url(path string) string { return f.URL + path }

func (f *websiteFixture) hitCount(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits[path]
}

func (f *websiteFixture) lastHeaders(path string) http.Header {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.headers[path]
}

type websiteEnv struct {
	deps WebsiteDeps
	obs  *recordingObserver
	urls *fakeWebsiteURL
}

func newWebsiteEnv(t *testing.T, blacklist func(string) bool, remoteXML, trustAPIForExtras, sizeOnly bool) *websiteEnv {
	t.Helper()
	hx, err := httpx.New(httpx.Config{UserAgent: "goggo-test/1.0"})
	if err != nil {
		t.Fatalf("httpx.New: %v", err)
	}
	env := &websiteEnv{obs: &recordingObserver{}, urls: &fakeWebsiteURL{res: map[string]resolveResult{}}}
	env.deps = WebsiteDeps{
		HTTP:              hx,
		URL:               env.urls,
		Observer:          env.obs,
		Blacklist:         blacklist,
		XMLDirectory:      filepath.Join(t.TempDir(), "xml"),
		RemoteXML:         remoteXML,
		TrustAPIForExtras: trustAPIForExtras,
		SizeOnly:          sizeOnly,
	}
	return env
}

// hasMessage reports whether one recorded message carries every token. It lets
// a test ask the question once, instead of looping and then re-asking the
// environment for the same list to build the failure message.
//
// The tokens are the contract — the decision word and the file it names — and
// the sentence around them is not: "Skipping complete file" and "setup.bin"
// must land in the same message, while their punctuation and connectives are
// free to change.
func hasMessage(messages []string, tokens ...string) bool {
	for _, text := range messages {
		all := true
		for _, token := range tokens {
			if !strings.Contains(text, token) {
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

func (e *websiteEnv) messageTexts() []string {
	var out []string
	for _, ev := range e.obs.events {
		if ev.Kind == EventMessageInfo || ev.Kind == EventMessageWarning ||
			ev.Kind == EventMessageError || ev.Kind == EventMessageSuccess {
			out = append(out, ev.Text)
		}
	}
	return out
}

// TestRunWebsiteEmptyTasks locks the immediate return: no observer call happens
// for an empty website task list.
func TestRunWebsiteEmptyTasks(t *testing.T) {
	env := newWebsiteEnv(t, nil, false, false, false)
	if err := RunWebsite(context.Background(), nil, Options{}, env.deps); err != nil {
		t.Fatalf("RunWebsite: %v", err)
	}
	if len(env.obs.events) != 0 {
		t.Errorf("events = %+v, want none", env.obs.events)
	}
}

// TestRunWebsiteBlacklistSkip locks the per-file filter: a blacklisted path is
// announced and skipped without a single request.
func TestRunWebsiteBlacklistSkip(t *testing.T) {
	f := newWebsiteFixture(t)
	dest := filepath.Join(t.TempDir(), "skipped.bin")
	f.set("/file", "content")

	env := newWebsiteEnv(t, func(path string) bool { return true }, false, false, false)
	env.urls.set("skipped.bin", resolveResult{downlink: f.url("/file")})

	if err := RunWebsite(context.Background(), []model.WebsiteTask{
		{Destination: dest, DownlinkURL: "/downlink", Gamename: "game"},
	}, Options{Workers: 1}, env.deps); err != nil {
		t.Fatalf("RunWebsite: %v", err)
	}
	if hits := f.hitCount("/file"); hits != 0 {
		t.Errorf("requests = %d, want none for a blacklisted file", hits)
	}
	if messages := env.messageTexts(); !hasMessage(messages, "Blacklisted", filepath.Base(dest)) {
		t.Errorf("messages = %v, want the blacklist line naming %s", messages, filepath.Base(dest))
	}
}

// TestRunWebsiteDirOccupiedSkip locks the directory branch: a file sitting at
// the destination's directory skips the task with a warning.
func TestRunWebsiteDirOccupiedSkip(t *testing.T) {
	f := newWebsiteFixture(t)
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(blocker, "file.bin")

	env := newWebsiteEnv(t, nil, false, false, false)
	env.urls.set("file.bin", resolveResult{downlink: f.url("/file")})
	f.set("/file", "content")

	if err := RunWebsite(context.Background(), []model.WebsiteTask{
		{Destination: dest, Gamename: "game"},
	}, Options{Workers: 1}, env.deps); err != nil {
		t.Fatalf("RunWebsite: %v", err)
	}
	if messages := env.messageTexts(); !hasMessage(messages, "not directory", "skipping file", filepath.Base(dest)) {
		t.Errorf("messages = %v, want the occupied-directory warning naming %s", messages, filepath.Base(dest))
	}
}

// TestRunWebsiteNoDownlink locks the two skip paths of an unusable downlink
// document: both are warnings, neither fails the run.
func TestRunWebsiteNoDownlink(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "file.bin")

	env := newWebsiteEnv(t, nil, false, false, false)
	env.urls.set("file.bin", resolveResult{err: fmt.Errorf("galaxy: %w", ErrEmptyDownlink)})
	if err := RunWebsite(context.Background(), []model.WebsiteTask{
		{Destination: dest, Gamename: "game"},
	}, Options{Workers: 1}, env.deps); err != nil {
		t.Fatalf("RunWebsite: %v", err)
	}
	if messages := env.messageTexts(); !hasMessage(messages, ErrEmptyDownlink.Error()) {
		t.Errorf("messages = %v, want the empty-downlink warning", messages)
	}

	env2 := newWebsiteEnv(t, nil, false, false, false)
	env2.urls.set("file.bin", resolveResult{err: fmt.Errorf("galaxy: %w", ErrNoDownlink)})
	if err := RunWebsite(context.Background(), []model.WebsiteTask{
		{Destination: dest, Gamename: "game"},
	}, Options{Workers: 1}, env2.deps); err != nil {
		t.Fatalf("RunWebsite: %v", err)
	}
	if messages := env2.messageTexts(); !hasMessage(messages, ErrNoDownlink.Error()) {
		t.Errorf("messages = %v, want the no-downlink warning", messages)
	}
}

// TestRunWebsitePlainDownload locks the plain path: the resolved url is fetched
// and the file holds its content.
func TestRunWebsitePlainDownload(t *testing.T) {
	f := newWebsiteFixture(t)
	dest := filepath.Join(t.TempDir(), "game", "readme.txt")

	env := newWebsiteEnv(t, nil, false, false, false)
	env.urls.set("readme.txt", resolveResult{downlink: f.url("/file")})
	f.set("/file", "file content")

	if err := RunWebsite(context.Background(), []model.WebsiteTask{
		{Destination: dest, Gamename: "game"},
	}, Options{Workers: 1}, env.deps); err != nil {
		t.Fatalf("RunWebsite: %v", err)
	}
	assertFileContent(t, dest, "file content")
}

// TestRunWebsiteChecksummedCompleteSkip locks the installer version check: a
// local file whose md5 matches the checksum document and whose size matches its
// total_size is skipped without a download request.
func TestRunWebsiteChecksummedCompleteSkip(t *testing.T) {
	f := newWebsiteFixture(t)
	dest := filepath.Join(t.TempDir(), "game", "setup.bin")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	content := "installer v1"
	if err := os.WriteFile(dest, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	checksumXML := checksumDoc("setup.bin", content)

	env := newWebsiteEnv(t, nil, true, false, false)
	env.urls.set("setup.bin", resolveResult{downlink: f.url("/file"), checksumXML: checksumXML})
	f.set("/file", content)

	if err := RunWebsite(context.Background(), []model.WebsiteTask{
		{Destination: dest, DownlinkURL: "/downlink", Gamename: "game", Checksummed: true, Size: "100"},
	}, Options{Workers: 1}, env.deps); err != nil {
		t.Fatalf("RunWebsite: %v", err)
	}
	if hits := f.hitCount("/file"); hits != 0 {
		t.Errorf("requests = %d, want none for a complete file", hits)
	}
	if messages := env.messageTexts(); !hasMessage(messages, "Skipping complete file", "setup.bin") {
		t.Errorf("messages = %v, want the skip line naming setup.bin", messages)
	}
	// The remote checksum document is cached under the xml directory.
	if _, err := os.Stat(filepath.Join(env.deps.XMLDirectory, "game", "setup.bin.xml")); err != nil {
		t.Errorf("the checksum document was not saved: %v", err)
	}
}

// TestRunWebsiteCreateXMLRepairsUnusableRemote locks the C1 semantics: a remote
// checksum document that fails the manifest rules counts as no document — it is
// neither cached verbatim (an unusable file would shadow regeneration) nor does
// it suppress the generated manifest under --create-xml.
func TestRunWebsiteCreateXMLRepairsUnusableRemote(t *testing.T) {
	f := newWebsiteFixture(t)
	dest := filepath.Join(t.TempDir(), "game", "setup.bin")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	const content = "installer bytes"
	f.set("/file", content)

	// Declares two chunks but carries one: valid XML, unusable manifest.
	const unusable = `<file name="setup.bin" chunks="2" total_size="15" md5="00000000000000000000000000000000"><chunk id="0" from="0" to="14" method="md5">00000000000000000000000000000000</chunk></file>`

	env := newWebsiteEnv(t, nil, true, false, false)
	env.deps.CreateXML = true
	env.deps.ChunkSize = 1 << 20
	env.urls.set("setup.bin", resolveResult{downlink: f.url("/file"), checksumXML: unusable})

	if err := RunWebsite(context.Background(), []model.WebsiteTask{
		{Destination: dest, DownlinkURL: "/downlink", Gamename: "game", Checksummed: true},
	}, Options{Workers: 1}, env.deps); err != nil {
		t.Fatalf("RunWebsite: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(env.deps.XMLDirectory, "game", "setup.bin.xml"))
	if err != nil {
		t.Fatalf("no usable manifest was cached: %v", err)
	}
	if string(data) == unusable {
		t.Fatal("the unusable remote document was cached verbatim")
	}
	if _, perr := gogxml.Parse(bytes.NewReader(data)); perr != nil {
		t.Errorf("the cached manifest is not valid: %v", perr)
	}
}

// TestRunWebsiteVersionRename locks the different-version branch: the local
// file moves to the dated.old name and the new content downloads.
func TestRunWebsiteVersionRename(t *testing.T) {
	f := newWebsiteFixture(t)
	dest := filepath.Join(t.TempDir(), "game", "setup.bin")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte("installer v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	checksumXML := checksumDoc("setup.bin", "installer v2")

	env := newWebsiteEnv(t, nil, true, false, false)
	env.urls.set("setup.bin", resolveResult{downlink: f.url("/file"), checksumXML: checksumXML})
	f.set("/file", "installer v2")

	if err := RunWebsite(context.Background(), []model.WebsiteTask{
		{Destination: dest, DownlinkURL: "/downlink", Gamename: "game", Checksummed: true},
	}, Options{Workers: 1}, env.deps); err != nil {
		t.Fatalf("RunWebsite: %v", err)
	}
	assertFileContent(t, dest, "installer v2")

	matches, _ := filepath.Glob(dest + ".*.old")
	if len(matches) != 1 {
		t.Fatalf(".old files = %v, want exactly one", matches)
	}
	assertFileContent(t, matches[0], "installer v1")
}

// TestRunWebsiteResume locks the resume branch: a partial local file with a
// cached checksum document of the same version continues from its own size, and
// the assembled file is the whole content.
func TestRunWebsiteResume(t *testing.T) {
	f := newWebsiteFixture(t)
	full := "installer v2 body"
	dest := filepath.Join(t.TempDir(), "game", "setup.bin")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte("insta"), 0o644); err != nil {
		t.Fatal(err)
	}
	checksumXML := checksumDoc("setup.bin", full)

	// The cached document from the interrupted run makes the fast check report
	// the remote md5, so the partial file counts as the same version.
	xmlDir := filepath.Join(t.TempDir(), "xml")
	if err := os.MkdirAll(filepath.Join(xmlDir, "game"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(xmlDir, "game", "setup.bin.xml"),
		[]byte(checksumXML), 0o644); err != nil {
		t.Fatal(err)
	}

	env := newWebsiteEnv(t, nil, true, false, false)
	env.deps.XMLDirectory = xmlDir
	env.urls.set("setup.bin", resolveResult{downlink: f.url("/file"), checksumXML: checksumXML})
	f.set("/file", full)

	if err := RunWebsite(context.Background(), []model.WebsiteTask{
		{Destination: dest, DownlinkURL: "/downlink", Gamename: "game", Checksummed: true},
	}, Options{Workers: 1}, env.deps); err != nil {
		t.Fatalf("RunWebsite: %v", err)
	}
	assertFileContent(t, dest, full)

	// The request resumed from the partial file's size.
	h := f.lastHeaders("/file")
	if got := h.Get("Range"); got != "bytes=5-" {
		t.Errorf("Range = %q, want %q", got, "bytes=5-")
	}
}

// TestRunWebsiteRetryAndCleanupMatrix locks the failure cleanup classes: a
// transport break keeps the partial file, an HTTP failure on a fresh download
// removes it, and a retry that recovers still completes the file. Every
// subtest gets its own destination: the classes interact through what is left
// on disk, so sharing one would couple them.
func TestRunWebsiteRetryAndCleanupMatrix(t *testing.T) {
	t.Run("transport failure keeps the partial file", func(t *testing.T) {
		// A raw TCP server writes a partial body and closes the connection: the
		// client's read fails mid-transfer, which is the PARTIAL_FILE class
		// whose partial file is kept for a later resume. (httptest's hijacked
		// connections proved unreliable at producing that read failure here.)
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()
		go func() {
			for {
				conn, err := ln.Accept()
				if err != nil {
					return
				}
				go func(c net.Conn) {
					c.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 100\r\n\r\npartial"))
					c.Close()
				}(conn)
			}
		}()

		env := newWebsiteEnv(t, nil, false, false, false)
		env.urls.set("data.bin", resolveResult{downlink: "http://" + ln.Addr().String() + "/file"})
		dest := filepath.Join(t.TempDir(), "data.bin")
		if err := RunWebsite(context.Background(), []model.WebsiteTask{
			{Destination: dest, Gamename: "game"},
		}, Options{Workers: 1, Retries: 1}, env.deps); err != nil {
			t.Fatalf("RunWebsite = %v, want nil: task failures are events", err)
		}
		if fi, err := os.Stat(dest); err != nil || fi.Size() == 0 {
			t.Errorf("partial file = %v, want it kept for a later resume", err)
		}
		// The failed attempt leaves as the "Download complete (<err>): <name>"
		// warning, not as an error.
		var sawFailure bool
		for _, ev := range env.obs.events {
			if ev.Kind == EventMessageWarning && strings.Contains(ev.Text, "Download complete (") {
				sawFailure = true
			}
		}
		if !sawFailure {
			t.Errorf("events = %+v, want the download-complete warning", env.obs.events)
		}
	})

	t.Run("http failure on a fresh download removes the file", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "boom", http.StatusInternalServerError)
		}))
		defer srv.Close()

		env := newWebsiteEnv(t, nil, false, false, false)
		env.urls.set("data.bin", resolveResult{downlink: srv.URL + "/file"})
		dest := filepath.Join(t.TempDir(), "data.bin")
		if err := RunWebsite(context.Background(), []model.WebsiteTask{
			{Destination: dest, Gamename: "game"},
		}, Options{Workers: 1, Retries: 1}, env.deps); err != nil {
			t.Fatalf("RunWebsite = %v, want nil", err)
		}
		assertFileAbsent(t, dest)
	})

	t.Run("a retry that recovers completes the file", func(t *testing.T) {
		var calls int
		var mu sync.Mutex
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			calls++
			n := calls
			mu.Unlock()
			if n == 1 {
				http.Error(w, "boom", http.StatusInternalServerError)
				return
			}
			w.Write([]byte("recovered"))
		}))
		defer srv.Close()

		env := newWebsiteEnv(t, nil, false, false, false)
		env.urls.set("data.bin", resolveResult{downlink: srv.URL + "/file"})
		dest := filepath.Join(t.TempDir(), "data.bin")
		if err := RunWebsite(context.Background(), []model.WebsiteTask{
			{Destination: dest, Gamename: "game"},
		}, Options{Workers: 1, Retries: 3}, env.deps); err != nil {
			t.Fatalf("RunWebsite: %v", err)
		}
		assertFileContent(t, dest, "recovered")
		if messages := env.messageTexts(); !hasMessage(messages, "Retry", "1/3", filepath.Base(dest)) {
			t.Errorf("messages = %v, want the retry line for %s", messages, filepath.Base(dest))
		}
	})
}

// TestRunWebsiteFiletime locks that the server's Last-Modified moves onto the
// downloaded file.
func TestRunWebsiteFiletime(t *testing.T) {
	f := newWebsiteFixture(t)
	lm := time.Date(2020, 5, 4, 3, 2, 1, 0, time.UTC)
	f.set("/file", "content")
	f.mu.Lock()
	f.bodies["/file"] = "content"
	f.mu.Unlock()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Last-Modified", lm.Format(http.TimeFormat))
		w.Write([]byte("content"))
	}))
	defer srv.Close()

	env := newWebsiteEnv(t, nil, false, false, false)
	env.urls.set("data.bin", resolveResult{downlink: srv.URL + "/file"})
	dest := filepath.Join(t.TempDir(), "data.bin")

	if err := RunWebsite(context.Background(), []model.WebsiteTask{
		{Destination: dest, Gamename: "game"},
	}, Options{Workers: 1}, env.deps); err != nil {
		t.Fatalf("RunWebsite: %v", err)
	}
	fi, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.ModTime().UTC(); got.Year() != 2020 || got.Month() != time.May {
		t.Errorf("mtime = %v, want the Last-Modified value %v", got, lm)
	}
}

// assertFileAbsent reads path and fails when it exists.
func assertFileAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err == nil {
		t.Errorf("%s exists, want it gone", path)
	}
}

// md5Of is the hex md5 of a string, for the checksum documents of the fixtures.
func md5Of(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

// checksumDoc builds a valid one-chunk checksum document for content: the shape
// the live API sends (the manifest rules reject a document whose chunk list
// contradicts its own header, and these fixtures stand for real documents).
func checksumDoc(name, content string) string {
	return fmt.Sprintf(`<file name="%s" chunks="1" total_size="%d" md5="%s"><chunk id="0" from="0" to="%d" method="md5">%s</chunk></file>`,
		name, len(content), md5Of(content), len(content)-1, md5Of(content))
}

// TestWebsiteTaskResultVerdicts locks the aggregation seam: the callback sees one
// verdict per task — nil for a success and for an authorised skip, non-nil for
// the exhausted-retries failure — and the run itself still ends without error so
// the other tasks keep their chance (the aggregate exit contract rides on these
// verdicts, not on the event texts).
func TestWebsiteTaskResultVerdicts(t *testing.T) {
	f := newWebsiteFixture(t)
	f.set("/good.bin", "payload")
	env := newWebsiteEnv(t, func(path string) bool { return strings.Contains(path, "black.bin") }, false, true, false)
	dir := t.TempDir()
	// The fake provider resolves by destination base name: good.bin points
	// at served bytes, bad.bin at a path the fixture answers with 404.
	env.urls.set("good.bin", resolveResult{downlink: f.url("/good.bin")})
	env.urls.set("bad.bin", resolveResult{downlink: f.url("/nope.bin")})

	var (
		mu       sync.Mutex
		verdicts map[string]error
	)
	verdicts = map[string]error{}
	env.deps.TaskResult = func(task model.WebsiteTask, err error) {
		mu.Lock()
		defer mu.Unlock()
		verdicts[task.Destination] = err
	}

	tasks := []model.WebsiteTask{
		{Destination: filepath.Join(dir, "good.bin"), DownlinkURL: f.url("/good.bin"), Gamename: "g", Size: "6"},
		{Destination: filepath.Join(dir, "bad.bin"), DownlinkURL: f.url("/bad.bin"), Gamename: "g", Size: "6"},
		{Destination: filepath.Join(dir, "black.bin"), DownlinkURL: f.url("/good.bin"), Gamename: "g", Size: "6"},
	}
	if err := RunWebsite(context.Background(), tasks, Options{Workers: 1, Retries: 0}, env.deps); err != nil {
		t.Fatalf("RunWebsite = %v, want the run to end", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(verdicts) != 3 {
		t.Fatalf("verdicts = %d, want one per task", len(verdicts))
	}
	if err := verdicts[filepath.Join(dir, "good.bin")]; err != nil {
		t.Errorf("good task = %v, want nil", err)
	}
	if err := verdicts[filepath.Join(dir, "bad.bin")]; err == nil {
		t.Error("failed task = nil, want the operational error reported")
	}
	if err := verdicts[filepath.Join(dir, "black.bin")]; err != nil {
		t.Errorf("blacklisted skip = %v, want nil (an authorised skip)", err)
	}
}

// TestDownloadArtifact locks the direct-link primitive: served bytes land at the
// destination with the server timestamp attempt semantics, a 404 fails and leaves
// nothing, and there is NO exists-skip — the second run re-downloads.
func TestDownloadArtifact(t *testing.T) {
	f := newWebsiteFixture(t)
	f.set("/logo.jpg", "jpeg-bytes")
	hx, err := httpx.New(httpx.Config{UserAgent: "goggo-test/1.0"})
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "logo_game.jpg")

	if err := DownloadArtifact(context.Background(), f.url("/logo.jpg"), dest, Options{Retries: 0}, ArtifactDeps{HTTP: hx}); err != nil {
		t.Fatalf("DownloadArtifact: %v", err)
	}
	body, err := os.ReadFile(dest)
	if err != nil || string(body) != "jpeg-bytes" {
		t.Fatalf("artifact body = %q (%v)", body, err)
	}

	// No exists-skip: a second run hits the server again.
	if err := DownloadArtifact(context.Background(), f.url("/logo.jpg"), dest, Options{Retries: 0}, ArtifactDeps{HTTP: hx}); err != nil {
		t.Fatalf("second DownloadArtifact: %v", err)
	}
	if hits := f.hitCount("/logo.jpg"); hits != 2 {
		t.Errorf("requests = %d, want the artifact re-downloaded", hits)
	}

	// A 404 fails and removes the file the attempt truncated.
	if err := DownloadArtifact(context.Background(), f.url("/gone.jpg"), dest, Options{Retries: 0}, ArtifactDeps{HTTP: hx}); err == nil {
		t.Fatal("404 err = nil, want failure")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Error("the failed attempt left a truncated file behind")
	}

	if err := DownloadArtifact(context.Background(), "https://x/y", "z", Options{}, ArtifactDeps{}); err == nil {
		t.Error("nil HTTP client accepted, want the dependency refusal")
	}
}
