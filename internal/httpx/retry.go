package httpx

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// ShouldRetry decides whether a failed attempt should be retried. resp is
// non-nil only when the transport delivered an HTTP response; err is non-nil
// for transport-level failures. Business layers plug their own policy here
// (OAuth may refresh on 401 and retry, a manifest fetch may retry 5xx, a
// chunk download may stop on 404).
type ShouldRetry func(resp *http.Response, err error) bool

// RetryPolicy configures the DoWithRetry decorator. MaxAttempts is the total
// number of attempts (>= 1); Wait is the pause inserted before every retry.
type RetryPolicy struct {
	ShouldRetry ShouldRetry
	MaxAttempts int
	Wait        time.Duration
}

// DefaultShouldRetry retries transport-level errors, retries HTTP errors except
// 403/404, and never retries a canceled or deadline-exceeded context.
//
// It is tuned for these APIs rather than being a general HTTP retry policy. In
// particular it retries 401, which OAuth flows must override (refresh the token,
// then re-issue the request) instead of blindly re-sending.
func DefaultShouldRetry(resp *http.Response, err error) bool {
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return false
		}
		return true
	}
	if resp == nil || resp.StatusCode < 400 {
		return false
	}
	return resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusForbidden
}

// DoWithRetry runs fn until it succeeds or the policy says stop. It returns
// the final *http.Response (owned by the caller, body untouched) or the final
// transport error. A response with a retryable status that survives all
// attempts is still returned so the caller can inspect it; bodies of
// responses that are retried are drained and closed to reuse the connection.
func DoWithRetry(ctx context.Context, policy RetryPolicy, fn func(context.Context) (*http.Response, error)) (*http.Response, error) {
	if policy.MaxAttempts < 1 {
		policy.MaxAttempts = 1
	}
	if policy.ShouldRetry == nil {
		policy.ShouldRetry = DefaultShouldRetry
	}

	for attempt := 1; ; attempt++ {
		if attempt > 1 && policy.Wait > 0 {
			if err := sleepCtx(ctx, policy.Wait); err != nil {
				return nil, err
			}
		}
		resp, err := fn(ctx)
		if err != nil {
			if attempt >= policy.MaxAttempts || !policy.ShouldRetry(nil, err) {
				return nil, err
			}
			continue
		}
		if resp == nil {
			return nil, errors.New("httpx: fn returned nil response and nil error")
		}
		if attempt >= policy.MaxAttempts || !policy.ShouldRetry(resp, nil) {
			return resp, nil
		}
		resp.Body.Close()
	}
}

// sleepCtx waits for d or returns ctx.Err if the context ends first.
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
