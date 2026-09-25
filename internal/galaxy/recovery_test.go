package galaxy

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/nekrozis/goggo/internal/httpx"
)

// The recovery matrix for a rejected API session. The shapes pinned here:
// recovery runs at most once per request, a retry that is rejected again
// terminates, recovery failure never retries the request, and every non-401
// answer keeps its original error. T3 is the loop guard: a future edit that
// reauthorizes inside an unbounded retry dies there.

// statusScript serves the given statuses in order (the last one repeats) and
// counts the requests it saw.
type statusScript struct {
	statuses []int
	mu       sync.Mutex
	got      int
}

func (s *statusScript) handler(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.got
	s.got++
	if i >= len(s.statuses) {
		i = len(s.statuses) - 1
	}
	if s.statuses[i] == http.StatusOK {
		w.Write([]byte(`{}`))
		return
	}
	w.WriteHeader(s.statuses[i])
}

func (s *statusScript) requests() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.got
}

func recoveryClient(t *testing.T, script *statusScript, reauth Reauthorizer) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(script.handler))
	t.Cleanup(srv.Close)
	cl := newTestClient(t, srv, map[string]any{"access_token": "t", "expires_at": float64(4102444800)})
	if reauth != nil {
		cl.reauth = reauth
	}
	cl.ep.api = srv.URL
	cl.ep.contentSystem = srv.URL
	return cl
}

func fetch(c *Client) error {
	_, err := c.getResponseBytes(context.Background(), c.ep.api+"/products?ids=1")
	return err
}

func TestRecoverySucceedsAfterReauth(t *testing.T) {
	script := &statusScript{statuses: []int{http.StatusUnauthorized, http.StatusOK}}
	calls := 0
	cl := recoveryClient(t, script, func(context.Context) error { calls++; return nil })
	if err := fetch(cl); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if calls != 1 || script.requests() != 2 {
		t.Fatalf("reauth=%d requests=%d, want 1 and 2", calls, script.requests())
	}
}

func TestRecoveryFailureIsSessionRejectedWithoutRetry(t *testing.T) {
	script := &statusScript{statuses: []int{http.StatusUnauthorized}}
	refreshErr := errors.New("refresh: HTTP 400")
	cl := recoveryClient(t, script, func(context.Context) error { return refreshErr })
	err := fetch(cl)
	var rej *SessionRejectedError
	if !errors.As(err, &rej) {
		t.Fatalf("error %v, want *SessionRejectedError", err)
	}
	if !errors.Is(err, refreshErr) {
		t.Errorf("Unwrap chain lost the refresh cause: %v", err)
	}
	if script.requests() != 1 {
		t.Errorf("requests=%d, want 1: a failed recovery must not retry", script.requests())
	}
	if got := err.Error(); got != "GOG API rejected the session (HTTP 401). Run 'goggo auth login' to authenticate again." {
		t.Errorf("wording = %q", got)
	}
}

func TestSecondRejectionTerminatesRecovery(t *testing.T) {
	script := &statusScript{statuses: []int{http.StatusUnauthorized}}
	calls := 0
	cl := recoveryClient(t, script, func(context.Context) error { calls++; return nil })
	err := fetch(cl)
	var rej *SessionRejectedError
	if !errors.As(err, &rej) {
		t.Fatalf("error %v, want *SessionRejectedError", err)
	}
	var se *httpx.StatusError
	if !errors.As(err, &se) || se.Code != http.StatusUnauthorized {
		t.Errorf("cause should be the second 401, got %v", err)
	}
	if calls != 1 || script.requests() != 2 {
		t.Fatalf("reauth=%d requests=%d, want 1 and 2: recovery must run at most once", calls, script.requests())
	}
}

func TestNilReauthorizerKeepsStatusError(t *testing.T) {
	script := &statusScript{statuses: []int{http.StatusUnauthorized}}
	cl := recoveryClient(t, script, nil)
	err := fetch(cl)
	var se *httpx.StatusError
	if !errors.As(err, &se) || se.Code != 401 {
		t.Fatalf("error %v, want raw *httpx.StatusError 401", err)
	}
	var rej *SessionRejectedError
	if errors.As(err, &rej) {
		t.Error("disabled recovery must not produce SessionRejectedError")
	}
}

func TestOtherStatusesNeverReauthorize(t *testing.T) {
	for _, code := range []int{http.StatusForbidden, http.StatusInternalServerError} {
		script := &statusScript{statuses: []int{code}}
		calls := 0
		cl := recoveryClient(t, script, func(context.Context) error { calls++; return nil })
		err := fetch(cl)
		var se *httpx.StatusError
		if !errors.As(err, &se) || se.Code != code {
			t.Fatalf("code %d: error %v, want raw status error", code, err)
		}
		if calls != 0 {
			t.Errorf("code %d: reauth ran %d times, want 0", code, calls)
		}
	}
}

func TestSuccessNeverReauthorizes(t *testing.T) {
	script := &statusScript{statuses: []int{http.StatusOK}}
	calls := 0
	cl := recoveryClient(t, script, func(context.Context) error { calls++; return nil })
	if err := fetch(cl); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if calls != 0 {
		t.Errorf("reauth ran %d times on a 200, want 0", calls)
	}
}
