package httpx

import (
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

// ErrLowSpeed reports that a transfer was aborted because it stayed below the
// configured rate. The sentinel carries the identity and the wrap the numbers,
// e.g. "httpx: transfer stalled: below 200 B/s for 30s"; the message never
// contains the request URL (see SafeError).
var ErrLowSpeed = errors.New("httpx: transfer stalled")

// Low-speed guard defaults. They are owned by the transport layer: a caller that
// configures nothing still gets a guard, and Config.DisableLowSpeedGuard is the
// explicit off switch, so a zero value is never load-bearing for the semantics.
const (
	// DefaultLowSpeedLimit is the abort threshold in bytes per second.
	DefaultLowSpeedLimit = 200
	// DefaultLowSpeedTime is how long the average rate may stay below the
	// limit before the transfer is aborted.
	DefaultLowSpeedTime = 30 * time.Second
)

// belowLimit reports whether a window that moved bytes over elapsed is slower
// than limit. The comparison is strict: exactly limit is NOT slow.
func belowLimit(bytes int64, elapsed time.Duration, limit int64) bool {
	if elapsed <= 0 {
		return false
	}
	return float64(bytes)/elapsed.Seconds() < float64(limit)
}

// lowSpeedBody is a response body with a low-speed watchdog.
//
// The clock starts on the FIRST Read, so a body that is never read is never
// judged slow. The average rate is evaluated when the window elapses: on a Read
// that carried data, and on the watchdog timer for the case no Read can observe
// — a body that stopped producing bytes, where the timer closes the body to
// unblock the pending Read. A window that meets the limit restarts, so an early
// burst cannot mask a later stall the way a whole-transfer average would; EOF
// and underlying errors pass through untouched.
//
// A Read that ends the transfer retires the watchdog, so a late timer cannot
// turn a completed transfer into a stall. An abort records ErrLowSpeed BEFORE
// closing the body, so the Read it unblocks reports ErrLowSpeed even if the
// underlying reader now fails with a close-induced error. State is mutex-guarded
// (Read on the caller's goroutine, the timer on the runtime's); Close is
// idempotent.
type lowSpeedBody struct {
	winStart time.Time
	body     io.ReadCloser
	aborted  error
	timer    *time.Timer
	now      func() time.Time
	window   time.Duration
	limit    int64
	total    int64
	winBase  int64
	mu       sync.Mutex
	done     bool
	finished bool
}

// newLowSpeedBody wraps body. limit and window must be positive: the caller
// resolves the defaults (Client.guardBody).
func newLowSpeedBody(body io.ReadCloser, limit int64, window time.Duration) *lowSpeedBody {
	return &lowSpeedBody{body: body, limit: limit, window: window, now: time.Now}
}

// Read implements io.Reader.
func (b *lowSpeedBody) Read(p []byte) (int, error) {
	n, err := b.body.Read(p)

	b.mu.Lock()
	defer b.mu.Unlock()

	if b.aborted != nil {
		// The watchdog already gave up: report its error (the caller gets the
		// n bytes that did arrive, then stops). The abort owns the outcome, so
		// a close-induced error from the underlying reader never replaces it.
		b.finished = true
		return n, b.aborted
	}
	if n > 0 {
		b.total += int64(n)
		switch {
		case b.winStart.IsZero():
			b.winStart = b.now()
			b.armLocked()
		case b.now().Sub(b.winStart) >= b.window:
			elapsed := b.now().Sub(b.winStart)
			if belowLimit(b.total-b.winBase, elapsed, b.limit) {
				b.aborted = b.stallErrorLocked()
				b.finished = true
				return n, b.aborted
			}
			b.winStart, b.winBase = b.now(), b.total
			b.armLocked()
		}
	}
	if err != nil {
		// The transfer is over, whatever the outcome: mark it finished so a
		// timer that already fired cannot abort after the fact, and retire the
		// watchdog.
		b.finished = true
		b.stopTimerLocked()
	}
	return n, err
}

// check runs on the timer goroutine. It evaluates a window that no Read could
// observe (the body stopped producing bytes, so Read is blocked) and, when the
// window is too slow, closes the underlying body to unblock that Read.
func (b *lowSpeedBody) check() {
	b.mu.Lock()
	if b.aborted != nil || b.done || b.finished || b.winStart.IsZero() {
		b.mu.Unlock()
		return
	}
	elapsed := b.now().Sub(b.winStart)
	if elapsed < b.window {
		b.armLocked()
		b.mu.Unlock()
		return
	}
	if !belowLimit(b.total-b.winBase, elapsed, b.limit) {
		b.winStart, b.winBase = b.now(), b.total
		b.armLocked()
		b.mu.Unlock()
		return
	}
	b.stopTimerLocked()
	b.aborted = b.stallErrorLocked()
	b.mu.Unlock()

	// Closing outside the lock: it is what unblocks the pending Read, and the
	// reader needs the lock as soon as it returns.
	_ = b.body.Close()
}

// Close stops the watchdog and closes the underlying body. It is idempotent.
func (b *lowSpeedBody) Close() error {
	b.mu.Lock()
	if b.done {
		b.mu.Unlock()
		return nil
	}
	b.done = true
	b.stopTimerLocked()
	b.mu.Unlock()
	return b.body.Close()
}

// stallErrorLocked builds the aborted-transfer error. The message carries the
// numbers but never the URL.
func (b *lowSpeedBody) stallErrorLocked() error {
	return fmt.Errorf("%w: below %d B/s for %s", ErrLowSpeed, b.limit, b.window)
}

// armLocked (re)arms the watchdog; the first call creates the timer.
func (b *lowSpeedBody) armLocked() {
	if b.timer == nil {
		b.timer = time.AfterFunc(b.window, b.check)
		return
	}
	b.timer.Reset(b.window)
}

// stopTimerLocked retires the watchdog. It is safe to call repeatedly.
func (b *lowSpeedBody) stopTimerLocked() {
	if b.timer != nil {
		b.timer.Stop()
	}
}
