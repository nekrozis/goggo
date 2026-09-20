package httpx

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// guardClient builds a client whose watchdog uses test-sized thresholds, so the
// whole matrix runs in milliseconds instead of the 30 s production default.
func guardClient(t *testing.T, cfg Config) *Client {
	t.Helper()
	c, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

// flusher returns the response writer's Flusher, which the handlers below need
// to make small writes reach the client immediately (net/http buffers them).
func flusher(t *testing.T, w http.ResponseWriter) http.Flusher {
	t.Helper()
	fl, ok := w.(http.Flusher)
	if !ok {
		t.Fatal("ResponseWriter is not a Flusher")
	}
	return fl
}

// TestLowSpeedTrickleAborts covers the rate-based case: bytes keep arriving, but
// far below the limit, so the transfer is aborted.
func TestLowSpeedTrickleAborts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000")
		fl := flusher(t, w)
		for i := 0; i < 1000; i++ {
			if r.Context().Err() != nil {
				return // the client gave up
			}
			if _, err := w.Write([]byte("x")); err != nil {
				return
			}
			fl.Flush()
			time.Sleep(20 * time.Millisecond) // ≈50 B/s
		}
	}))
	defer srv.Close()

	c := guardClient(t, Config{LowSpeedLimit: 500, LowSpeedTime: 100 * time.Millisecond})
	_, err := c.GetBytes(context.Background(), srv.URL)
	if !errors.Is(err, ErrLowSpeed) {
		t.Fatalf("err = %v, want ErrLowSpeed", err)
	}
	// The message must not leak the request URL.
	if msg := err.Error(); strings.Contains(msg, srv.URL) || strings.Contains(msg, "http://") {
		t.Errorf("error %q must not carry the URL", msg)
	}
	if msg := err.Error(); !strings.Contains(msg, "httpx: transfer stalled") {
		t.Errorf("error = %q, want the sentinel text", msg)
	}
}

// TestLowSpeedHardStallAborts covers the case no Read can observe: the body
// stops producing bytes, so the watchdog timer must close the body and unblock
// the pending read. This is the behaviour the step exists for.
func TestLowSpeedHardStallAborts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		if _, err := w.Write([]byte("start")); err != nil {
			return
		}
		flusher(t, w).Flush()
		<-r.Context().Done() // stall until the client goes away
	}))
	defer srv.Close()

	c := guardClient(t, Config{LowSpeedLimit: 1000, LowSpeedTime: 150 * time.Millisecond})
	start := time.Now()
	_, err := c.GetBytes(context.Background(), srv.URL)
	elapsed := time.Since(start)

	if !errors.Is(err, ErrLowSpeed) {
		t.Fatalf("err = %v (after %v), want ErrLowSpeed", err, elapsed)
	}
	if elapsed > 5*time.Second {
		t.Errorf("abort took %v, want it close to the window", elapsed)
	}
}

// TestLowSpeedAbortKeepsReportingItself covers the losing side of the race: once
// the watchdog has aborted, every later Read must keep reporting ErrLowSpeed
// rather than a close-induced error from the underlying body.
func TestLowSpeedAbortKeepsReportingItself(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		if _, err := w.Write([]byte("start")); err != nil {
			return
		}
		flusher(t, w).Flush()
		<-r.Context().Done()
	}))
	defer srv.Close()

	c := guardClient(t, Config{LowSpeedLimit: 1000, LowSpeedTime: 100 * time.Millisecond})
	resp, err := c.Get(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()

	if _, ok := resp.Body.(*lowSpeedBody); !ok {
		t.Fatalf("Do must install the watchdog, got %T", resp.Body)
	}
	// Drain until the watchdog fires.
	if _, err := io.ReadAll(resp.Body); !errors.Is(err, ErrLowSpeed) {
		t.Fatalf("first error = %v, want ErrLowSpeed", err)
	}
	// The underlying reader now fails with whatever the close produced; the
	// wrapper must keep answering with its own error.
	for i := 0; i < 3; i++ {
		if _, err := resp.Body.Read(make([]byte, 8)); !errors.Is(err, ErrLowSpeed) {
			t.Fatalf("read %d = %v, want ErrLowSpeed", i+1, err)
		}
	}
}

// TestLowSpeedCompletedTransferKeepsEOF covers the other side of the race: a
// transfer that ends first must not be turned into a stall by a late timer, and
// reading past EOF must keep reporting EOF.
func TestLowSpeedCompletedTransferKeepsEOF(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hello"))
	}))
	defer srv.Close()

	// Limit/窗口 small enough that a late timer would fire during the sleep
	// below if the watchdog were not retired.
	c := guardClient(t, Config{LowSpeedLimit: 1 << 20, LowSpeedTime: 50 * time.Millisecond})
	resp, err := c.Get(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()

	body, ok := resp.Body.(*lowSpeedBody)
	if !ok {
		t.Fatalf("Do must install the watchdog, got %T", resp.Body)
	}
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("body = %q, want %q", got, "hello")
	}

	time.Sleep(3 * 50 * time.Millisecond) // several windows

	body.mu.Lock()
	aborted, finished := body.aborted, body.finished
	body.mu.Unlock()
	if aborted != nil {
		t.Errorf("a completed transfer must not be aborted later: %v", aborted)
	}
	if !finished {
		t.Error("an ended transfer must be marked finished")
	}
	if _, err := resp.Body.Read(make([]byte, 8)); !errors.Is(err, io.EOF) {
		t.Errorf("read past EOF = %v, want io.EOF", err)
	}
}

// TestLowSpeedNormalRateReadsFully covers a transfer well above the limit: it
// must complete with every byte intact.
func TestLowSpeedNormalRateReadsFully(t *testing.T) {
	payload := make([]byte, 1<<20)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	// Production defaults: 200 B/s over 30 s.
	c := guardClient(t, Config{})
	got, err := c.GetBytes(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("GetBytes: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload mismatch: got %d bytes, want %d", len(got), len(payload))
	}
}

// TestLowSpeedShortTransferNotAborted covers a transfer that finishes inside the
// window: 5 bytes would be far below any sensible rate, but the window never
// elapses, and the final short window must not be reported as a stall.
func TestLowSpeedShortTransferNotAborted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hello"))
	}))
	defer srv.Close()

	c := guardClient(t, Config{LowSpeedLimit: 1 << 20, LowSpeedTime: 200 * time.Millisecond})
	got, err := c.GetBytes(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("GetBytes: %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("body = %q, want %q", got, "hello")
	}
}

// TestLowSpeedDisabledDoesNotAbort covers the explicit off switch: even a
// transfer that would trip the configured thresholds completes.
func TestLowSpeedDisabledDoesNotAbort(t *testing.T) {
	const payload = "0123456789"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "10")
		if _, err := w.Write([]byte(payload[:1])); err != nil {
			return
		}
		flusher(t, w).Flush()
		time.Sleep(300 * time.Millisecond) // far below the configured limit
		_, _ = w.Write([]byte(payload[1:]))
	}))
	defer srv.Close()

	c := guardClient(t, Config{
		DisableLowSpeedGuard: true,
		LowSpeedLimit:        1 << 20,
		LowSpeedTime:         20 * time.Millisecond,
	})
	got, err := c.GetBytes(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("GetBytes: %v (the guard must be off)", err)
	}
	if string(got) != payload {
		t.Errorf("body = %q, want %q", got, payload)
	}
}

// TestLowSpeedNoReadThenCloseIsInert covers the unread-body case: the window
// must not start before the first Read, and Close must retire the watchdog.
func TestLowSpeedNoReadThenCloseIsInert(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hello"))
	}))
	defer srv.Close()

	c := guardClient(t, Config{LowSpeedLimit: 1 << 20, LowSpeedTime: 50 * time.Millisecond})
	resp, err := c.Get(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	body, ok := resp.Body.(*lowSpeedBody)
	if !ok {
		t.Fatalf("Do must install the watchdog, got %T", resp.Body)
	}

	// No Read: waiting longer than several windows must not start a window.
	time.Sleep(3 * 50 * time.Millisecond)
	body.mu.Lock()
	started, aborted := !body.winStart.IsZero(), body.aborted
	body.mu.Unlock()
	if started {
		t.Error("the window must not start before the first Read")
	}
	if aborted != nil {
		t.Errorf("aborted without any Read: %v", aborted)
	}

	if err := resp.Body.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	time.Sleep(2 * 50 * time.Millisecond)
	body.mu.Lock()
	aborted, done := body.aborted, body.done
	body.mu.Unlock()
	if aborted != nil {
		t.Errorf("aborted after Close: %v", aborted)
	}
	if !done {
		t.Error("Close must retire the watchdog")
	}
}

// TestLowSpeedBoundaryIsStrict pins the threshold semantics without timing
// jitter: exactly the limit is not slow, anything below it is.
func TestLowSpeedBoundaryIsStrict(t *testing.T) {
	if belowLimit(200, time.Second, 200) {
		t.Error("exactly the limit must NOT count as slow")
	}
	if !belowLimit(199, time.Second, 200) {
		t.Error("just below the limit must count as slow")
	}
	if !belowLimit(0, time.Second, 200) {
		t.Error("no bytes in a full window must count as slow")
	}
	if belowLimit(0, 0, 200) {
		t.Error("a zero-length window is not judgeable and must not abort")
	}
}

// TestGuardBodyRespectsConfig covers the wiring: the wrapper appears exactly
// when the guard is enabled, and the resolved thresholds are the transport
// defaults when the config leaves them zero.
func TestGuardBodyRespectsConfig(t *testing.T) {
	enabled := guardClient(t, Config{})
	if !enabled.lowSpeedGuard {
		t.Error("the guard must be on by default")
	}
	if enabled.lowSpeedLimit != DefaultLowSpeedLimit || enabled.lowSpeedTime != DefaultLowSpeedTime {
		t.Errorf("defaults = %d B/s / %v, want %d / %v",
			enabled.lowSpeedLimit, enabled.lowSpeedTime, DefaultLowSpeedLimit, DefaultLowSpeedTime)
	}

	custom := guardClient(t, Config{LowSpeedLimit: 42, LowSpeedTime: 7 * time.Millisecond})
	if custom.lowSpeedLimit != 42 || custom.lowSpeedTime != 7*time.Millisecond {
		t.Errorf("configured = %d / %v, want 42 / 7ms", custom.lowSpeedLimit, custom.lowSpeedTime)
	}

	resp := &http.Response{Body: io.NopCloser(strings.NewReader("x"))}
	off := guardClient(t, Config{DisableLowSpeedGuard: true})
	off.guardBody(resp)
	if _, wrapped := resp.Body.(*lowSpeedBody); wrapped {
		t.Error("a disabled guard must not wrap the body")
	}
}
