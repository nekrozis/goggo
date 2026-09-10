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
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/nekrozis/goggo/internal/httpx"
	"github.com/nekrozis/goggo/internal/model"
)

// Run executes the plan's tasks: the worker fan-out, the per-chunk fetch and
// verification, and the event stream.
//
// Failure semantics are upstream's (review D65a): a task that fails — a URL
// that cannot be resolved, a chunk that exhausts its retries — reports itself
// as an EventMessageError and the remaining tasks continue; Run still returns
// nil when every task has ended, even if some failed. Only a cancelled context
// makes Run return an error.
//
// The per-chunk fetch is the d1 minimal body: GET the whole chunk, verify its
// compressed md5, decompress and append. The chunk-internal resume of a retry
// (CURLOPT_RESUME_FROM_LARGE) and the finer failure cleanup are S20; a retry
// re-downloads the whole chunk.
//
// The scheduling itself lives in schedule: worker fan-out, the single
// deliverer and the cancellation handling are shared with the website path
// (review D67).
func Run(ctx context.Context, tasks []model.FileTask, opts Options, deps RunDeps) error {
	if deps.HTTP == nil || deps.URL == nil || deps.Observer == nil {
		return errors.New("transfer: run needs an http client, a url provider and an observer")
	}
	return schedule(ctx, tasks, opts.Workers,
		func(ev Event) { deps.Observer.OnEvent(ev) },
		func(ctx context.Context, task model.FileTask, emit func(Event)) error {
			return runChunkTask(ctx, task, opts, deps, emit)
		})
}

// runChunkTask downloads one task: the parent directories, then every chunk in
// order, each fetched, verified and appended (downloader.cpp:4450-4790). A
// failure ends this task and is returned; other tasks continue. The failure is
// also emitted as an error event, so the caller's non-nil return only matters
// for a cancelled context.
func runChunkTask(ctx context.Context, task model.FileTask, opts Options, deps RunDeps, emit func(Event)) error {
	emit(Event{Path: task.Destination, ChunkCount: len(task.Item.Chunks), Kind: EventTaskStart})

	fail := func(text string) error {
		emit(Event{Path: task.Destination, Text: text, Kind: EventMessageError})
		emit(Event{Path: task.Destination, Kind: EventTaskFinish})
		return errors.New(text)
	}

	if err := os.MkdirAll(filepath.Dir(task.Destination), 0o755); err != nil {
		return fail("Failed to create directory: " + err.Error())
	}

	// An item without chunks is an empty file (downloader.cpp:4644-4650).
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

	for j, chunk := range task.Item.Chunks {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := downloadChunk(ctx, task, j, chunk, opts, deps, emit); err != nil {
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

// downloadChunk fetches one chunk, verifies its compressed md5 and appends the
// decompressed bytes to the task's file, retrying the whole chunk on failure
// (downloader.cpp:4673-4744). The URL is resolved once per chunk; a retry
// re-performs the same URL. Upstream resumes a retried curl transfer from the
// bytes it already has (CURLOPT_RESUME_FROM_LARGE, downloader.cpp:4695); that
// refinement is S20 — here a retry re-downloads the whole chunk.
func downloadChunk(ctx context.Context, task model.FileTask, index int, chunk model.GalaxyDepotItemChunk, opts Options, deps RunDeps, emit func(Event)) error {
	label := fmt.Sprintf("%s (chunk %d/%d)", task.Destination, index+1, len(task.Item.Chunks))

	url, err := deps.URL.URL(ctx, task, chunk)
	if err != nil {
		return err
	}

	reason := ""
	for attempt := 0; ; attempt++ {
		// The delay precedes every attempt, including the first
		// (downloader.cpp:4682-4684).
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

		body, err := fetchChunkBody(ctx, deps.HTTP, url)
		if err == nil {
			sum := md5.Sum(body)
			if hex.EncodeToString(sum[:]) != chunk.CompressedMD5 {
				err = errors.New("chunk failed hash check")
			}
		}

		// A 416 is the one HTTP status that never retries
		// (downloader.cpp:4706-4712); everything else — transport errors and
		// any other status — does.
		if err == nil {
			if err := appendChunk(ctx, task.Destination, body); err != nil {
				return err
			}
			return nil
		}
		var status *httpx.StatusError
		if errors.As(err, &status) && status.Code == http.StatusRequestedRangeNotSatisfiable {
			return err
		}
		if attempt >= opts.Retries {
			return err
		}
		reason = err.Error()
	}
}

// fetchChunkBody performs one GET and returns the response body. It uses the
// non-retrying Do on purpose: the retry loop above owns the retry policy, the
// way curl_easy_perform does upstream.
func fetchChunkBody(ctx context.Context, hx *httpx.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := hx.Do(ctx, req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, &httpx.StatusError{Method: http.MethodGet, URL: url, Code: resp.StatusCode}
	}
	return io.ReadAll(resp.Body)
}

// appendChunk decompresses the zlib stream and appends the uncompressed bytes
// to the task's file (downloader.cpp:4756-4775). The file handle has a single
// owner: the Close whose error is returned is the only one.
func appendChunk(ctx context.Context, destination string, compressed []byte) error {
	zr, err := zlib.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return fmt.Errorf("zlib: %w", err)
	}
	defer zr.Close()
	f, err := os.OpenFile(destination, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, zr); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
