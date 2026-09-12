package transfer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"crypto/md5"
	"encoding/hex"

	"github.com/nekrozis/goggo/internal/httpx"
	"github.com/nekrozis/goggo/internal/model"
)

// countingCDN serves static bodies per path and counts hits, so the resume
// tests can prove which chunks were actually fetched.
type countingCDN struct {
	*httptest.Server

	mu        sync.Mutex
	bodies    map[string][]byte
	hitCounts map[string]int
}

func newCountingCDN(t *testing.T) *countingCDN {
	t.Helper()
	c := &countingCDN{bodies: map[string][]byte{}, hitCounts: map[string]int{}}
	c.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		c.hitCounts[r.URL.Path]++
		body, ok := c.bodies[r.URL.Path]
		c.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(body)
	}))
	t.Cleanup(c.Close)
	return c
}

func (c *countingCDN) set(path string, body []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.bodies[path] = body
}

func (c *countingCDN) hits(path string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hitCounts[path]
}

func (c *countingCDN) url(path string) string { return c.URL + path }

// totalHits sums every request the server has served, for "the CDN was never
// touched" assertions.
func (c *countingCDN) totalHits() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	total := 0
	for _, n := range c.hitCounts {
		total += n
	}
	return total
}

// countingDeps wires the counting CDN into the run: every chunk resolves to
// its own body on the CDN, so per-chunk hit counts prove what was fetched.
func countingDeps(t *testing.T, cdn *countingCDN, obs *recordingObserver) RunDeps {
	t.Helper()
	hx, err := httpx.New(httpx.Config{UserAgent: "goggo-test/1.0"})
	if err != nil {
		t.Fatalf("httpx.New: %v", err)
	}
	return RunDeps{HTTP: hx, URL: urlFunc(func(_ context.Context, _ model.FileTask, chunk model.GalaxyDepotItemChunk) (string, error) {
		return cdn.url("/c/" + chunk.CompressedMD5), nil
	}), Observer: obs}
}

// buildChunkedItem splits content into equal uncompressed parts, compresses
// each into a chunk and returns the item with every offset/total computed, plus
// the compressed bodies keyed by the chunk's compressed md5.
func buildChunkedItemParts(t *testing.T, parts int, content string) (model.GalaxyDepotItem, map[string][]byte) {
	t.Helper()
	if len(content)%parts != 0 {
		t.Fatalf("content %d is not divisible by %d parts", len(content), parts)
	}
	partLen := len(content) / parts

	var item model.GalaxyDepotItem
	item.Path = "game/file.bin"
	var total, totalCompressed uint64
	bodies := map[string][]byte{}
	for i := 0; i < parts; i++ {
		part := content[i*partLen : (i+1)*partLen]
		compressed := compress(t, part)
		compressedMD5 := md5OfBytes(compressed)
		contentMD5 := md5OfBytes([]byte(part))
		bodies["/c/"+compressedMD5] = compressed

		item.Chunks = append(item.Chunks, model.GalaxyDepotItemChunk{
			CompressedMD5:  compressedMD5,
			MD5:            contentMD5,
			CompressedSize: uint64(len(compressed)),
			Size:           uint64(len(part)),
			Offset:         total,
		})
		// total tracks the UNCOMPRESSED offset (the destination file holds
		// decompressed bytes), totalCompressed the compressed one.
		total += uint64(len(part))
		totalCompressed += uint64(len(compressed))
	}
	item.TotalSize = uint64(len(content))
	item.MD5 = md5hex([]byte(content))
	item.TotalCompressedSize = total
	return item, bodies
}

// TestRunSkipsCompleteFile locks the transfer-level skip: a destination that
// already satisfies the item produces no HTTP request at all, and the task
// closes with the ": OK" success message (upstream 4497).
func md5hex(data []byte) string {
	sum := md5.Sum(data)
	return hex.EncodeToString(sum[:])
}

func TestRunSkipsCompleteFile(t *testing.T) {
	content := "already complete"
	cdn := newCountingCDN(t)
	item, _ := buildChunkedItemParts(t, 1, content)

	dest := filepath.Join(t.TempDir(), "file.bin")
	if err := os.WriteFile(dest, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	obs := &recordingObserver{}
	if err := Run(context.Background(), []model.FileTask{{Item: item, Destination: dest}}, Options{}, countingDeps(t, cdn, obs)); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := cdn.hits("/c/" + item.Chunks[0].CompressedMD5); got != 0 {
		t.Errorf("chunk hits = %d, want 0 (nothing to transfer)", got)
	}
	var sawOK, sawFinish bool
	for _, ev := range obs.events {
		if ev.Kind == EventMessageSuccess && strings.Contains(ev.Text, ": OK") {
			sawOK = true
		}
		if ev.Path == dest && ev.Kind == EventTaskFinish {
			sawFinish = true
		}
	}
	if !sawOK || !sawFinish {
		t.Errorf("events = %+v, want the OK success message and TaskFinish", obs.events)
	}
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != content {
		t.Errorf("file = %q, want %q (untouched)", data, content)
	}
}

// TestRunResumesFromLastBoundary is the core RES1 evidence: chunks 0-1 are
// already on disk, so only the last chunk is fetched and the assembled file is
// exactly the manifest content.
func TestRunResumesFromLastBoundary(t *testing.T) {
	// Three uncompressed chunks of 1000 bytes each, so the partial file can sit
	// exactly on the chunk-2 boundary.
	content := strings.Repeat("a", 1000) + strings.Repeat("b", 1000) + strings.Repeat("c", 1000)
	cdn := newCountingCDN(t)
	item, bodies := buildChunkedItemParts(t, 3, content)
	for _, ch := range item.Chunks {
		cdn.set("/c/"+ch.CompressedMD5, bodies["/c/"+ch.CompressedMD5])
	}

	dest := filepath.Join(t.TempDir(), "file.bin")
	if err := os.WriteFile(dest, []byte(content[:2000]), 0o644); err != nil {
		t.Fatal(err)
	}
	obs := &recordingObserver{}
	if err := Run(context.Background(), []model.FileTask{{Item: item, Destination: dest}}, Options{}, countingDeps(t, cdn, obs)); err != nil {
		t.Fatalf("Run: %v", err)
	}

	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != content {
		t.Errorf("file = %q, want %q", data, content)
	}
	// Only the last chunk was fetched: the first two were already on disk.
	for i, ch := range item.Chunks {
		if i < 2 && cdn.hits("/c/"+ch.CompressedMD5) != 0 {
			t.Errorf("chunk %d was fetched although its bytes were already on disk", i)
		}
	}
	if got := cdn.hits("/c/" + item.Chunks[2].CompressedMD5); got != 1 {
		t.Errorf("last chunk hits = %d, want 1", got)
	}
}

// TestRunReplacesMismatchedCompleteFile locks the replace branch: a complete
// file whose whole-file md5 does not match the item is removed and downloaded
// again — never appended to.
func TestRunReplacesMismatchedCompleteFile(t *testing.T) {
	content := "the real content"
	cdn := newCountingCDN(t)
	item, bodies := buildChunkedItemParts(t, 2, content)
	for _, ch := range item.Chunks {
		cdn.set("/c/"+ch.CompressedMD5, bodies["/c/"+ch.CompressedMD5])
	}

	dest := filepath.Join(t.TempDir(), "file.bin")
	obs := &recordingObserver{}
	stale := "same size, wrong content....."
	if err := os.WriteFile(dest, []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Run(context.Background(), []model.FileTask{{Item: item, Destination: dest}}, Options{}, countingDeps(t, cdn, obs)); err != nil {
		t.Fatalf("Run: %v", err)
	}

	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != content {
		t.Errorf("file = %q, want %q", data, content)
	}
}

// TestRunReplacesOversizedFile locks the oversize branch: a destination longer
// than the item is removed before the download, not appended to.
func TestRunReplacesOversizedFile(t *testing.T) {
	content := "bounded content"
	cdn := newCountingCDN(t)
	item, bodies := buildChunkedItemParts(t, 1, content)
	for _, ch := range item.Chunks {
		cdn.set("/c/"+ch.CompressedMD5, bodies["/c/"+ch.CompressedMD5])
	}

	dest := filepath.Join(t.TempDir(), "file.bin")
	obs := &recordingObserver{}
	if err := os.WriteFile(dest, []byte(content+" plus stale trailing bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Run(context.Background(), []model.FileTask{{Item: item, Destination: dest}}, Options{}, countingDeps(t, cdn, obs)); err != nil {
		t.Fatalf("Run: %v", err)
	}

	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != content {
		t.Errorf("file = %q, want %q", data, content)
	}
}

// TestRunReplacesInvalidBoundary locks the boundary verification: a partial
// file whose size lands on a chunk boundary but whose preceding chunk no
// longer matches its uncompressed md5 is replaced, not resumed.
func TestRunReplacesInvalidBoundary(t *testing.T) {
	content := strings.Repeat("a", 1000) + strings.Repeat("b", 1000) + strings.Repeat("c", 1000)
	cdn := newCountingCDN(t)
	item, bodies := buildChunkedItemParts(t, 3, content)
	for _, ch := range item.Chunks {
		cdn.set("/c/"+ch.CompressedMD5, bodies["/c/"+ch.CompressedMD5])
	}

	dest := filepath.Join(t.TempDir(), "file.bin")
	obs := &recordingObserver{}
	// 2000 bytes on the chunk-2 boundary, but the first 1000 bytes are not
	// chunk 0's uncompressed content: the boundary is invalid.
	if err := os.WriteFile(dest, []byte("XX"+content[2:1000]), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Run(context.Background(), []model.FileTask{{Item: item, Destination: dest}}, Options{}, countingDeps(t, cdn, obs)); err != nil {
		t.Fatalf("Run: %v", err)
	}

	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != content {
		t.Errorf("file = %q, want the full re-download to produce %q", data, content)
	}
}

// TestRunUncompressedMD5MismatchFailsWithoutAppend locks the chunk commit
// prerequisite (review RES1 v2 §13): when the decompressed content does not
// match the chunk's uncompressed md5, nothing is appended and the task fails
// with a verification error — no retry can fix a manifest/content mismatch.
func TestRunUncompressedMD5MismatchFailsWithoutAppend(t *testing.T) {
	cdn := newCountingCDN(t)
	body := compress(t, "whatever the server sends")
	cdn.set("/c/"+md5OfBytes(body), body)
	chunk := model.GalaxyDepotItemChunk{
		// The manifest's compressed md5 matches the served body, but the
		// uncompressed md5 is deliberately wrong.
		CompressedMD5:  md5OfBytes(body),
		MD5:            "deliberately-wrong-uncompressed-md5",
		CompressedSize: uint64(len(body)),
		Size:           uint64(len(body)),
	}
	dest := filepath.Join(t.TempDir(), "file.bin")
	obs := &recordingObserver{}

	// D65a: the failed task leaves as an error event; Run itself is nil.
	if err := Run(context.Background(), singleTask(dest, chunk), Options{Workers: 1, Retries: 2}, countingDeps(t, cdn, obs)); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var sawVerifyFailure bool
	for _, ev := range obs.events {
		if ev.Kind == EventMessageError && strings.Contains(ev.Text, "uncompressed content verification failed") {
			sawVerifyFailure = true
		}
	}
	if !sawVerifyFailure {
		t.Errorf("events = %+v, want the uncompressed verification failure", obs.events)
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Error("destination exists after the failed chunk, want no append")
	}
}

// TestRunCreatesMissingEmptyFile locks the zero-size path end to end (review
// RES1-R1 T2b): an item with no chunks and a missing destination still goes
// through the transfer as a task, touches no CDN byte, and ends as an empty
// file on disk — the plan must never have classified it as skipped.
func TestRunCreatesMissingEmptyFile(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "empty.bin")
	cdn := newCountingCDN(t)
	obs := &recordingObserver{}
	item := model.GalaxyDepotItem{Path: "game/empty.bin"} // TotalSize == 0, no chunks

	err := Run(context.Background(), []model.FileTask{{Item: item, Destination: dest}},
		Options{Workers: 1, Retries: 2}, countingDeps(t, cdn, obs))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	fi, err := os.Stat(dest)
	if err != nil {
		t.Fatalf("the empty file was not created: %v", err)
	}
	if fi.Size() != 0 {
		t.Errorf("size = %d, want 0", fi.Size())
	}
	if total := cdn.totalHits(); total != 0 {
		t.Errorf("CDN hits = %d, want 0 for a chunkless item", total)
	}
}
