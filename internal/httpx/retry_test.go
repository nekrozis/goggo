package httpx

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// sequenceServer answers with the given statuses in order, then keeps the
// last one. It counts requests.
func sequenceServer(t *testing.T, statuses ...int) (*httptest.Server, *int32) {
	var calls int32
	idx := int32(0)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		i := atomic.LoadInt32(&idx)
		atomic.StoreInt32(&idx, i+1)
		code := statuses[0]
		if int(i) < len(statuses) {
			code = statuses[i]
		}
		if code == http.StatusOK {
			fmt.Fprint(w, "ok")
			return
		}
		w.WriteHeader(code)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func TestDoWithRetrySuccessAfterFailures(t *testing.T) {
	srv, calls := sequenceServer(t,
		http.StatusInternalServerError,
		http.StatusInternalServerError,
		http.StatusOK,
	)
	c, _ := New(testConfig())
	policy := DefaultPolicy(3, time.Millisecond)
	resp, err := DoWithRetry(context.Background(), policy, func(ctx context.Context) (*http.Response, error) {
		return c.Get(ctx, srv.URL)
	})
	if err != nil {
		t.Fatalf("DoWithRetry: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("final status = %d", resp.StatusCode)
	}
	if got := atomic.LoadInt32(calls); got != 3 {
		t.Errorf("attempts = %d, want 3", got)
	}
}

func TestDoWithRetryExhaustedReturnsFinalResponse(t *testing.T) {
	srv, calls := sequenceServer(t,
		http.StatusInternalServerError,
		http.StatusInternalServerError,
		http.StatusInternalServerError,
	)
	c, _ := New(testConfig())
	policy := DefaultPolicy(3, time.Millisecond)
	resp, err := DoWithRetry(context.Background(), policy, func(ctx context.Context) (*http.Response, error) {
		return c.Get(ctx, srv.URL)
	})
	if err != nil {
		t.Fatalf("DoWithRetry returned err %v, want final response", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("final status = %d, want 500", resp.StatusCode)
	}
	if got := atomic.LoadInt32(calls); got != 3 {
		t.Errorf("attempts = %d, want 3", got)
	}
}

func TestGetBytesWithRetry404SingleAttempt(t *testing.T) {
	srv, calls := sequenceServer(t, http.StatusNotFound)
	cfg := testConfig()
	cfg.RetryPolicy = DefaultPolicy(5, 0)
	c, _ := New(cfg)
	_, err := c.GetBytesWithRetry(context.Background(), srv.URL)
	if !IsNotFound(err) {
		t.Errorf("err = %v, want IsNotFound", err)
	}
	if got := atomic.LoadInt32(calls); got != 1 {
		t.Errorf("attempts = %d, want 1", got)
	}
}

func TestGetBytesWithRetryStatusErrorAfterExhaustion(t *testing.T) {
	srv, calls := sequenceServer(t,
		http.StatusBadGateway,
		http.StatusBadGateway,
		http.StatusBadGateway,
	)
	cfg := testConfig()
	cfg.RetryPolicy = DefaultPolicy(3, time.Millisecond)
	c, _ := New(cfg)
	_, err := c.GetBytesWithRetry(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected StatusError")
	}
	var se *StatusError
	if !errors.As(err, &se) || se.Code != http.StatusBadGateway {
		t.Errorf("err = %v, want StatusError 502", err)
	}
	if got := atomic.LoadInt32(calls); got != 3 {
		t.Errorf("attempts = %d, want 3", got)
	}
}

func TestDoWithRetryTransportErrors(t *testing.T) {
	okSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok")
	}))
	defer okSrv.Close()

	var attempts int32
	policy := DefaultPolicy(3, time.Millisecond)
	resp, err := DoWithRetry(context.Background(), policy, func(ctx context.Context) (*http.Response, error) {
		if atomic.AddInt32(&attempts, 1) < 3 {
			return nil, errors.New("boom")
		}
		return http.Get(okSrv.URL) //nolint:gosec // test only
	})
	if err != nil {
		t.Fatalf("DoWithRetry: %v", err)
	}
	resp.Body.Close()
	if got := atomic.LoadInt32(&attempts); got != 3 {
		t.Errorf("attempts = %d, want 3", got)
	}
}

func TestDefaultShouldRetryMatrix(t *testing.T) {
	cases := []struct {
		code int
		want bool
	}{
		{http.StatusBadRequest, true},   // C++ retries every >=400 except 403/404
		{http.StatusUnauthorized, true}, // default policy retries 401 too; OAuth overrides later
		{http.StatusRequestTimeout, true},
		{http.StatusTooManyRequests, true},
		{http.StatusInternalServerError, true},
		{http.StatusBadGateway, true},
		{http.StatusServiceUnavailable, true},
		{http.StatusForbidden, false},
		{http.StatusNotFound, false},
		{http.StatusOK, false},
	}
	for _, tc := range cases {
		resp := &http.Response{StatusCode: tc.code}
		if got := DefaultShouldRetry(resp, nil); got != tc.want {
			t.Errorf("DefaultShouldRetry(%d) = %v, want %v", tc.code, got, tc.want)
		}
	}
	if DefaultShouldRetry(nil, context.Canceled) {
		t.Error("context.Canceled must not be retried")
	}
	if DefaultShouldRetry(nil, context.DeadlineExceeded) {
		t.Error("context.DeadlineExceeded must not be retried")
	}
	if !DefaultShouldRetry(nil, errors.New("net")) {
		t.Error("transport error must be retried")
	}
}

func TestDoWithRetryContextCancelDuringWait(t *testing.T) {
	var attempts int32
	policy := DefaultPolicy(10, 50*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := DoWithRetry(ctx, policy, func(ctx context.Context) (*http.Response, error) {
		atomic.AddInt32(&attempts, 1)
		return nil, errors.New("slow")
	})
	if err == nil {
		t.Fatal("expected error after context cancel")
	}
	if got := atomic.LoadInt32(&attempts); got > 2 {
		t.Errorf("attempts = %d, context cancel should stop early", got)
	}
}
