package transfer

import (
	"bytes"
	"compress/zlib"
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/nekrozis/goggo/internal/httpx"
	"github.com/nekrozis/goggo/internal/model"
	"github.com/nekrozis/goggo/internal/reconcile"
)

// Run executes the plan's tasks: the worker fan-out, the per-chunk fetch and
// verification, and the event stream.
//
// A task that fails — a URL that cannot be resolved, a chunk that exhausts its
// retries — reports itself as an EventMessageError and the remaining tasks
// continue. Run returns nil once every task has ended, even if some failed; only
// a cancelled context makes it return an error. The scheduling itself lives in
// schedule, shared with the website path.
func Run(ctx context.Context, tasks []model.FileTask, opts Options, deps RunDeps) error {
	if deps.HTTP == nil || deps.URL == nil || deps.Observer == nil {
		return errors.New("transfer: run needs an http client, a url provider and an observer")
	}
	// The queue snapshot is a run-level fact, published once before any task is
	// dispatched: readers derive what is still pending from the tasks they have
	// seen start. An empty run publishes too, so "no snapshot"
	// and "empty queue" stay distinguishable.
	deps.Progress.setQueue(len(tasks), queuedBytes(tasks))
	return schedule(ctx, tasks, opts.Workers,
		func(ev Event) { deps.Observer.OnEvent(ev) },
		func(ctx context.Context, task model.FileTask, emit func(Event)) error {
			return runChunkTask(ctx, task, opts, deps, emit)
		})
}

// queuedBytes sums the compressed size of every task in the run — the same
// basis the per-task totals and the progress events use.
func queuedBytes(tasks []model.FileTask) int64 {
	var sum int64
	for _, task := range tasks {
		sum += int64(task.Item.TotalCompressedSize)
	}
	return sum
}

// runChunkTask downloads one task: the parent directories, then every chunk in
// order, each fetched, verified and appended. A
// failure ends this task and is returned; other tasks continue. The failure is
// also emitted as an error event, so the caller's non-nil return only matters
// for a cancelled context.
func runChunkTask(ctx context.Context, task model.FileTask, opts Options, deps RunDeps, emit func(Event)) error {
	// The sampling slot lives exactly as long as the task and runs through the
	// deferred finish on every exit path — success, failure and cancellation
	// alike — so the registry never keeps a count that has stopped moving. The
	// slot is published before the start event, so a reader that sees the task
	// start can already read its total.
	slot := deps.Progress.start(task.Destination, int64(task.Item.TotalCompressedSize))
	defer deps.Progress.finish(task.Destination)

	emit(Event{Path: task.Destination, ChunkCount: len(task.Item.Chunks), Kind: EventTaskStart})

	fail := func(text string) error {
		emit(Event{Path: task.Destination, Text: text, Kind: EventMessageError})
		emit(Event{Path: task.Destination, Kind: EventTaskFinish})
		return errors.New(text)
	}

	if err := os.MkdirAll(filepath.Dir(task.Destination), 0o755); err != nil {
		return fail("Failed to create directory: " + err.Error())
	}

	// The authoritative reconcile: the plan's classification is advisory, the
	// destination's actual state decides what this task must do — including
	// skipping a file that was complete when the plan was built but changed
	// before the run reached it.
	decision, startChunk, err := reconcile.ReconcileExistingFile(task.Item, task.Destination)
	if err != nil {
		return fail("Failed to inspect " + task.Destination + ": " + err.Error())
	}
	switch decision {
	case reconcile.DecisionSkip:
		// The explicit skip marker: the front end counts dynamic skips by this
		// text, never by an inferred event sequence.
		emit(Event{Path: task.Destination, Text: SkipMessage(task.Destination), Kind: EventMessageSuccess})
		emit(Event{Path: task.Destination, Kind: EventTaskFinish})
		return nil
	case reconcile.DecisionReplace:
		emit(Event{Path: task.Destination, Text: "Replacing existing file: " + task.Destination, Kind: EventMessageInfo})
		if err := os.Remove(task.Destination); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fail("Failed to delete " + task.Destination + ": " + err.Error())
		}
		startChunk = 0
	case reconcile.DecisionResume:
		// The explicit resume marker: the only signal the front end counts
		// resumed tasks by — see observer.go.
		emit(Event{Path: task.Destination, Text: ResumeMessage(startChunk, task.Destination), Kind: EventMessageInfo})
	}

	// An item without chunks is an empty file.
	if len(task.Item.Chunks) == 0 {
		f, err := os.Create(task.Destination)
		if err != nil {
			return fail(err.Error())
		}
		if err := f.Close(); err != nil {
			return fail(err.Error())
		}
		emit(Event{Path: task.Destination, Kind: EventTaskFinish})
		return nil
	}

	for j := startChunk; j < len(task.Item.Chunks); j++ {
		chunk := task.Item.Chunks[j]
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := downloadChunk(ctx, task, j, chunk, opts, deps, slot, emit); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fail(err.Error())
		}
		emit(Event{
			Path: task.Destination, Current: int64(chunk.CompressedOffset + chunk.CompressedSize),
			Total: int64(task.Item.TotalCompressedSize), ChunkIndex: j,
			ChunkCount: len(task.Item.Chunks), Kind: EventProgress,
		})
	}
	emit(Event{Path: task.Destination, ChunkCount: len(task.Item.Chunks), Kind: EventTaskFinish})
	return nil
}

// downloadChunk fetches one chunk into memory, verifies its compressed md5 and
// appends the decompressed bytes to the task's file. The URL is resolved once
// per chunk; a retry re-performs the same URL.
//
// The two retry causes never mix: a transport failure resumes the next attempt
// from the bytes already in memory, with a Range header from the prefix length,
// while a hash mismatch empties the buffer so the retry starts the chunk from
// scratch — a corrupt prefix must not take part in the next hash.
func downloadChunk(ctx context.Context, task model.FileTask, index int, chunk model.GalaxyDepotItemChunk, opts Options, deps RunDeps, slot *progressSlot, emit func(Event)) error {
	label := fmt.Sprintf("%s (chunk %d/%d)", task.Destination, index+1, len(task.Item.Chunks))

	url, err := deps.URL.URL(ctx, task, chunk)
	if err != nil {
		return err
	}

	var body []byte
	reason := ""
	lastModified := time.Time{}
	for attempt := 0; ; attempt++ {
		// The delay precedes every attempt, including the first.
		if opts.Wait > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(opts.Wait):
			}
		}
		if attempt > 0 {
			emit(Event{Path: task.Destination,
				Text: fmt.Sprintf("Retry %d/%d: %s (%s)", attempt, opts.Retries, label, reason),
				Kind: EventMessageInfo})
		}

		resume := len(body) > 0
		// The attempt's logical starting point: the chunk's offset plus
		// whatever a previous attempt already buffered, bounded by the chunk's
		// own end. Publishing the start is also what turns a hash-mismatch
		// retry into a decrease — the buffer is empty again, so the value drops
		// back to the chunk's offset and the consumer restarts its window there.
		base := int64(chunk.CompressedOffset) + int64(len(body))
		slot.store(base)
		data, lm, code, err := fetchChunkBody(ctx, deps.HTTP, url, resume, len(body),
			progressSink{slot: slot, base: base, end: int64(chunk.CompressedOffset + chunk.CompressedSize)})
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !lm.IsZero() {
			lastModified = lm
		}

		if err != nil {
			// A transport break mid-body keeps whatever arrived: those bytes
			// are the prefix the next attempt resumes from. Status errors carry
			// no body to keep.
			var status *httpx.StatusError
			if !errors.As(err, &status) && len(data) > 0 {
				body = append(body, data...)
			}
		} else {
			// A server that ignores Range answers 200 with the whole body
			// again: folding it back to the suffix keeps the resume semantics.
			// The guard fires only for an attempt that actually asked for a
			// range past its first byte and got 200 back — a first attempt
			// and a 206 never take this path.
			if resume && code == http.StatusOK && len(data) >= len(body) {
				data = data[len(body):]
			}
			body = append(body, data...)
		}

		// A 416 keeps the buffer for the hash check below: the server has
		// nothing past the requested offset, so the bytes already in memory may
		// be the whole chunk. With an empty buffer a 416 fails the task
		// outright — a hash check over nothing can never succeed.
		var rangeNotSatisfiable bool
		if err != nil {
			var status *httpx.StatusError
			if errors.As(err, &status) && status.Code == http.StatusRequestedRangeNotSatisfiable {
				rangeNotSatisfiable = true
				if len(body) == 0 {
					return err
				}
			}
		}

		// The hash check runs on the whole buffer — after a complete response,
		// or after a 416 with the chunk already complete in memory.
		if err == nil || rangeNotSatisfiable {
			sum := md5.Sum(body)
			if hex.EncodeToString(sum[:]) != chunk.CompressedMD5 {
				// The corrupt prefix must not join the next hash: empty the
				// buffer so the retry starts from zero.
				body = nil
				err = errors.New("chunk failed hash check")
			}
		}

		if err == nil {
			if err := appendChunk(task.Destination, body, chunk.MD5); err != nil {
				return err
			}
			if !lastModified.IsZero() {
				// The server's timestamp moves onto the file; a failure to set
				// it is a warning, not a failed chunk. It is set per chunk, so
				// the value left behind is the last response's.
				if cerr := os.Chtimes(task.Destination, lastModified, lastModified); cerr != nil {
					emit(Event{Path: task.Destination, Text: cerr.Error(), Kind: EventMessageWarning})
				}
			}
			return nil
		}

		// Retry classification: transport errors, statuses other than 416, and
		// hash mismatches all retry.
		if attempt >= opts.Retries {
			return err
		}
		reason = err.Error()
	}
}

// fetchChunkBody performs one GET and returns the response body, the response
// code and the Last-Modified timestamp. It uses the non-retrying Do on purpose:
// the retry loop above owns the retry policy. When resumeLen is positive the
// request carries a Range header from that offset, and the code is returned so
// the caller can fold a 200 answer to a ranged request. sink reports the bytes as
// they arrive; its zero value reports nothing.
func fetchChunkBody(ctx context.Context, hx *httpx.Client, url string, resume bool, resumeLen int, sink progressSink) ([]byte, time.Time, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, time.Time{}, 0, httpx.SanitizeError(err)
	}
	if resume && resumeLen > 0 {
		req.Header.Set("Range", "bytes="+strconv.Itoa(resumeLen)+"-")
	}
	resp, err := hx.Do(ctx, req)
	if err != nil {
		return nil, time.Time{}, 0, err
	}
	defer resp.Body.Close()
	lm := lastModifiedFrom(resp)
	if resp.StatusCode >= 400 {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, lm, resp.StatusCode, httpx.NewStatusError(http.MethodGet, url, resp.StatusCode)
	}
	// The body streams into the buffer instead of ReadAll: a break mid-transfer
	// leaves the bytes already received in the buffer, which is what the caller
	// resumes from.
	var src io.Reader = resp.Body
	if sink.slot != nil {
		src = &progressReader{r: resp.Body, slot: sink.slot, base: sink.base, end: sink.end}
	}
	var buf bytes.Buffer
	_, cpErr := io.Copy(&buf, src)
	return buf.Bytes(), lm, resp.StatusCode, cpErr
}

// appendChunk decompresses the zlib stream, verifies the decompressed content
// against the chunk's uncompressed md5, and only then appends the whole chunk to
// the task's file. The file handle has a single owner: the Close whose error is
// returned is the only one.
//
// The verification-before-write order is the chunk transaction invariant: a
// chunk on disk is always a complete uncompressed chunk, which is what makes the
// next run's boundary reconcile reliable. An uncompressed md5 mismatch is not
// retried: the compressed bytes already passed their hash, so re-fetching the
// same chunk would produce the same result.
func appendChunk(destination string, compressed []byte, wantMD5 string) error {
	zr, err := zlib.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return fmt.Errorf("zlib: %w", err)
	}
	defer zr.Close()
	var decompressed bytes.Buffer
	if _, err := io.Copy(&decompressed, zr); err != nil {
		return fmt.Errorf("zlib decompress: %w", err)
	}
	if wantMD5 != "" {
		sum := md5.Sum(decompressed.Bytes())
		if hex.EncodeToString(sum[:]) != wantMD5 {
			return errors.New("uncompressed content verification failed (md5 mismatch after decompression)")
		}
	}
	f, err := os.OpenFile(destination, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(decompressed.Bytes()); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
