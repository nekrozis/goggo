package transfer

import (
	"bytes"
	"compress/zlib"
	"context"
	"crypto/md5"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/nekrozis/goggo/internal/httpx"
	"github.com/nekrozis/goggo/internal/model"
)

// pieceReader yields prepared pieces, one read at a time, so a test controls
// where the read boundaries land.
type pieceReader struct{ pieces [][]byte }

func (r *pieceReader) Read(p []byte) (int, error) {
	if len(r.pieces) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.pieces[0])
	r.pieces[0] = r.pieces[0][n:]
	if len(r.pieces[0]) == 0 {
		r.pieces = r.pieces[1:]
	}
	return n, nil
}

// TestProgressWithoutASlot locks the "no sampling state" contract: a nil
// registry, an unknown task and a nil slot all answer false rather than
// pretending the task is at zero bytes.
func TestProgressWithoutASlot(t *testing.T) {
	var nilProgress *Progress
	if v, ok := nilProgress.Bytes("x"); ok || v != 0 {
		t.Errorf("nil registry Bytes = (%d, %v), want (0, false)", v, ok)
	}
	if v, ok := nilProgress.Total("x"); ok || v != 0 {
		t.Errorf("nil registry Total = (%d, %v), want (0, false)", v, ok)
	}
	nilProgress.finish("x") // must not panic
	if slot := nilProgress.start("x", 1); slot != nil {
		t.Errorf("nil registry start = %v, want nil", slot)
	}
	nilProgress.setQueue(2, 8) // must not panic
	if tasks, bytes, ok := nilProgress.Queue(); ok || tasks != 0 || bytes != 0 {
		t.Errorf("nil registry Queue = (%d, %d, %v), want (0, 0, false)", tasks, bytes, ok)
	}
	var nilSlot *progressSlot
	nilSlot.store(5) // must not panic

	p := NewProgress()
	if _, ok := p.Bytes("unknown"); ok {
		t.Error("unknown task answered with a sample")
	}
	if _, ok := p.Total("unknown"); ok {
		t.Error("unknown task answered with a total")
	}
	if _, _, ok := p.Queue(); ok {
		t.Error("a registry that never ran answered with a queue snapshot")
	}
}

// TestProgressQueueSnapshot locks the run-level snapshot: Run publishes the task
// count and their summed compressed size before dispatching, and the snapshot
// outlives the run even though the task's own slot does not.
func TestProgressQueueSnapshot(t *testing.T) {
	cdn := newTestCDN(t)
	chunk := chunked(t, cdn, "queue snapshot")
	dest := filepath.Join(t.TempDir(), "file.bin")
	task := model.FileTask{
		Item: model.GalaxyDepotItem{
			Path:                "game/file.bin",
			TotalCompressedSize: chunk.CompressedSize,
			Chunks:              []model.GalaxyDepotItemChunk{chunk},
		},
		Destination: dest,
	}

	progress := NewProgress()
	deps := runDeps(t, cdn, &recordingObserver{})
	deps.Progress = progress
	if err := Run(context.Background(), []model.FileTask{task}, Options{}, deps); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if tasks, bytes, ok := progress.Queue(); !ok || tasks != 1 || bytes != int64(chunk.CompressedSize) {
		t.Errorf("Queue = (%d, %d, %v), want (1, %d, true)", tasks, bytes, ok, chunk.CompressedSize)
	}
	// The two layers answer differently on purpose: the task is over, so its
	// slot is gone, while the queue snapshot is a run-level fact.
	if _, ok := progress.Bytes(dest); ok {
		t.Error("Bytes answered after the task finished")
	}
	assertFileContent(t, dest, "queue snapshot")
}

// TestProgressQueueSnapshotEmptyRun locks that an empty run publishes an empty
// snapshot rather than publishing nothing: "no snapshot" and "empty queue" are
// different answers.
func TestProgressQueueSnapshotEmptyRun(t *testing.T) {
	hx, err := httpx.New(httpx.Config{UserAgent: "goggo-test/1.0"})
	if err != nil {
		t.Fatalf("httpx.New: %v", err)
	}
	progress := NewProgress()
	deps := RunDeps{
		HTTP:     hx,
		URL:      urlByChunk{cdn: newTestCDN(t)},
		Observer: &recordingObserver{},
		Progress: progress,
	}
	if err := Run(context.Background(), nil, Options{}, deps); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if tasks, bytes, ok := progress.Queue(); !ok || tasks != 0 || bytes != 0 {
		t.Errorf("Queue = (%d, %d, %v), want (0, 0, true)", tasks, bytes, ok)
	}
}

// TestProgressReaderPublishesAbsoluteProgress locks the reader's arithmetic:
// the published value is the attempt's base plus everything read so far, so a
// retry that resumes from the bytes already in memory stays monotone without an
// accumulator, and a hash reset legitimately hands the consumer a lower value.
func TestProgressReaderPublishesAbsoluteProgress(t *testing.T) {
	p := NewProgress()
	slot := p.start("task", 100)

	pr := &progressReader{r: &pieceReader{pieces: [][]byte{[]byte("abcd"), []byte("ef")}}, slot: slot, base: 10, end: 100}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(pr, buf); err != nil {
		t.Fatalf("first read: %v", err)
	}
	if v, ok := p.Bytes("task"); !ok || v != 14 {
		t.Errorf("after one read = (%d, %v), want (14, true)", v, ok)
	}
	if _, err := io.ReadFull(pr, buf[:2]); err != nil {
		t.Fatalf("second read: %v", err)
	}
	if v, _ := p.Bytes("task"); v != 16 {
		t.Errorf("after two reads = %d, want 16", v)
	}

	// A resume from the buffered prefix starts higher: still monotone.
	next := &progressReader{r: &pieceReader{pieces: [][]byte{[]byte("ghi")}}, slot: slot, base: 16, end: 100}
	if _, err := io.ReadFull(next, buf[:3]); err != nil {
		t.Fatalf("resumed read: %v", err)
	}
	if v, _ := p.Bytes("task"); v != 19 {
		t.Errorf("after the resumed read = %d, want 19", v)
	}

	// A hash mismatch empties the buffer, so the next attempt starts from the
	// chunk's offset again and the registry reports the lower value.
	slot.store(0)
	if v, ok := p.Bytes("task"); !ok || v != 0 {
		t.Errorf("after a reset = (%d, %v), want (0, true)", v, ok)
	}
}

// TestProgressReaderStopsAtTheChunkEnd locks the logical upper bound: a resume
// whose server ignores the Range and answers 200 with the whole chunk reads more
// bytes than the chunk has left, and the sample must stop at the chunk's end —
// past it, the caller's 200-fold would look like a decrease and reset the ETA
// window for no reason.
func TestProgressReaderStopsAtTheChunkEnd(t *testing.T) {
	p := NewProgress()
	slot := p.start("task", 2000)

	// Chunk offset 1000, a 400-byte prefix already buffered, and a server that
	// re-sends the full 1000-byte chunk: base + read would reach 2400.
	pr := &progressReader{
		r:    &pieceReader{pieces: [][]byte{bytes.Repeat([]byte("x"), 400), bytes.Repeat([]byte("y"), 600)}},
		slot: slot,
		base: 1400,
		end:  2000,
	}
	buf := make([]byte, 400)
	if _, err := io.ReadFull(pr, buf); err != nil {
		t.Fatalf("prefixed read: %v", err)
	}
	if v, _ := p.Bytes("task"); v != 1800 {
		t.Errorf("mid-attempt sample = %d, want 1800 (base + the bytes read)", v)
	}
	if _, err := io.ReadAll(pr); err != nil {
		t.Fatalf("rest of the body: %v", err)
	}
	if v, _ := p.Bytes("task"); v != 2000 {
		t.Errorf("sample after the over-long body = %d, want the chunk end 2000", v)
	}
}

// TestProgressTotalMatchesTheTaskTotal locks the Total constraint: it is the
// file's logical total — the same number the progress events carry — not a chunk
// size and not the size of the response body.
func TestProgressTotalMatchesTheTaskTotal(t *testing.T) {
	p := NewProgress()
	p.start("task", 4096)
	if v, ok := p.Total("task"); !ok || v != 4096 {
		t.Errorf("Total = (%d, %v), want (4096, true)", v, ok)
	}
	p.finish("task")
	if _, ok := p.Total("task"); ok {
		t.Error("Total answered after finish")
	}
}

// compressed returns the zlib stream a chunk body holds.
func compressed(t *testing.T, content string) []byte {
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

// arrivingBody is the response body the in-flight test reads through: it reports
// when the client comes back for more after a read that carried bytes. The
// previous read has been published to the sampler by then (the store happens
// before the next read is issued, in the same goroutine), so the test has a
// deterministic observation point instead of a polling loop.
type arrivingBody struct {
	body io.ReadCloser
	more chan struct{}
	read int
	once sync.Once
}

func (b *arrivingBody) Read(p []byte) (int, error) {
	if b.read > 0 {
		b.once.Do(func() { close(b.more) })
	}
	n, err := b.body.Read(p)
	b.read += n
	return n, err
}

func (b *arrivingBody) Close() error { return b.body.Close() }

// arrivingTransport installs that body on the network exit.
type arrivingTransport struct {
	base http.RoundTripper
	more chan struct{}
}

func (t arrivingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	resp.Body = &arrivingBody{body: resp.Body, more: t.more}
	return resp, nil
}

// TestProgressPublishesWhileTheChunkIsStillArriving locks the in-flight
// invariant: the sampler must see a transfer in flight, not only the chunk
// boundary the events report. The handler holds the second half of the body until
// the test has looked, and the transport reports the second read, so the partial
// reading is not a race and not a poll.
func TestProgressPublishesWhileTheChunkIsStillArriving(t *testing.T) {
	const content = "progress sampling payload"
	body := compressed(t, content)
	sum := md5.Sum(body)
	md5hex := hex.EncodeToString(sum[:])
	half := len(body) / 2
	if half == 0 {
		t.Fatal("body too small to split")
	}

	release := make(chan struct{})
	var once sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body[:half])
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-release
		w.Write(body[half:])
	}))
	defer srv.Close()
	defer once.Do(func() { close(release) })

	dest := filepath.Join(t.TempDir(), "file.bin")
	task := model.FileTask{
		Item: model.GalaxyDepotItem{
			Path:                "game/file.bin",
			TotalCompressedSize: uint64(len(body)),
			Chunks: []model.GalaxyDepotItemChunk{{
				CompressedMD5:  md5hex,
				CompressedSize: uint64(len(body)),
				Size:           uint64(len(content)),
			}},
		},
		Destination: dest,
	}

	more := make(chan struct{})
	hx, err := httpx.New(httpx.Config{
		UserAgent: "goggo-test/1.0",
		Transport: arrivingTransport{base: http.DefaultTransport, more: more},
	})
	if err != nil {
		t.Fatalf("httpx.New: %v", err)
	}
	progress := NewProgress()
	obs := &recordingObserver{}
	deps := RunDeps{
		HTTP: hx,
		URL: urlFunc(func(context.Context, model.FileTask, model.GalaxyDepotItemChunk) (string, error) {
			return srv.URL + "/chunk", nil
		}),
		Observer: obs,
		Progress: progress,
	}

	done := make(chan error, 1)
	go func() { done <- Run(context.Background(), []model.FileTask{task}, Options{}, deps) }()

	// The signal is a hang guard, not a timing assumption: the handler is
	// still holding the second half, so the sample cannot move past it.
	select {
	case <-more:
	case <-time.After(10 * time.Second):
		t.Fatal("the client never asked for a second read: no in-flight sample was possible")
	}
	middle, ok := progress.Bytes(dest)
	if !ok || middle <= 0 || middle >= int64(len(body)) {
		t.Errorf("in-flight sample = (%d, %v), want strictly inside (0, %d)", middle, ok, len(body))
	}
	// The total is available before the chunk has landed, which is what lets
	// the ETA exist during the first chunk.
	if v, ok := progress.Total(dest); !ok || v != int64(len(body)) {
		t.Errorf("Total while in flight = (%d, %v), want (%d, true)", v, ok, len(body))
	}
	// The display path still learns the task's progress from the events: the
	// sampler is not the display's feed.
	for _, ev := range obs.events {
		if ev.Kind != EventProgress {
			continue
		}
		if ev.Total != int64(len(body)) {
			t.Errorf("event Total = %d, want %d", ev.Total, len(body))
		}
	}

	once.Do(func() { close(release) })
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, ok := progress.Bytes(dest); ok {
		t.Error("slot survived the finished task")
	}
	if _, ok := progress.Total(dest); ok {
		t.Error("total survived the finished task")
	}
	assertFileContent(t, dest, content)
}

// TestRunWithoutProgressIsUnchanged locks the nil case: a run with no registry
// behaves exactly as before the surface existed — same plan, same file, no
// sampling state to report.
func TestRunWithoutProgressIsUnchanged(t *testing.T) {
	cdn := newTestCDN(t)
	chunk := chunked(t, cdn, "no registry here")
	dest := filepath.Join(t.TempDir(), "file.bin")
	obs := &recordingObserver{}

	if err := Run(context.Background(), singleTask(dest, chunk), Options{}, runDeps(t, cdn, obs)); err != nil {
		t.Fatalf("Run: %v", err)
	}
	assertFileContent(t, dest, "no registry here")
}
