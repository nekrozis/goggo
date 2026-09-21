package auth

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/nekrozis/goggo/internal/config"
	"github.com/nekrozis/goggo/internal/httpx"
)

// refreshServer returns a server whose /token handler records the query for
// assertions and writes resp (a JSON object) when non-nil.
func refreshServer(t *testing.T, resp map[string]any, status int) (*httptest.Server, *urlCaptor) {
	t.Helper()
	captor := &urlCaptor{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/token" {
			http.NotFound(w, r)
			return
		}
		captor.rawQuery = r.URL.RawQuery
		w.WriteHeader(status)
		if resp != nil {
			_ = json.NewEncoder(w).Encode(resp)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, captor
}

type urlCaptor struct{ rawQuery string }

func (c *urlCaptor) has(param, value string) bool {
	return strings.Contains(c.rawQuery, param+"="+value)
}

// newClientFor builds an auth Client whose token URL points at base's /token
// handler. Endpoint injection is instance-level (unexported tokenURL), never
// package-global.
func newClientFor(t *testing.T, base string) *Client {
	t.Helper()
	hx, err := httpx.New(httpx.Config{UserAgent: "goggo-test/1.0"})
	if err != nil {
		t.Fatalf("httpx.New: %v", err)
	}
	c := NewClient(hx)
	c.tokenURL = base + "/token"
	return c
}

// testStore returns a store holding one login response.
func testStore(t *testing.T, token map[string]any) *Store {
	t.Helper()
	s := newTestStore(t)
	s.StoreLoginResponse(token)
	return s
}

// TestRefreshErrorHidesCredentials: the refresh request URL carries
// client_secret and refresh_token, so a failing request must be rendered without
// it (httpx.SafeError). Without this, a future edit that goes back to %w would
// silently re-leak a ~30-day credential.
func TestRefreshErrorHidesCredentials(t *testing.T) {
	srv, cap := refreshServer(t, nil, http.StatusInternalServerError)
	c := newClientFor(t, srv.URL)
	s := testStore(t, tokenResponse(map[string]any{"refresh_token": "rt-secret"}))

	err := s.Refresh(context.Background(), c)
	if err == nil {
		t.Fatal("Refresh must fail on a 500 response")
	}
	// Precondition: the request really did carry the credential.
	if !cap.has("refresh_token", "rt-secret") {
		t.Fatalf("query %q must carry the refresh token", cap.rawQuery)
	}
	msg := err.Error()
	if !strings.Contains(msg, "auth: refresh token: HTTP 500") {
		t.Errorf("error = %q, want the sanitized status form", msg)
	}
	for _, leak := range []string{"rt-secret", "client_secret", "refresh_token=", "grant_type", "?client_id", "127.0.0.1"} {
		if strings.Contains(msg, leak) {
			t.Errorf("error %q leaks %q", msg, leak)
		}
	}
}

func TestRefreshSuccess(t *testing.T) {
	srv, cap := refreshServer(t, map[string]any{
		"access_token":  "at-2",
		"refresh_token": "rt-2",
		"expires_in":    3600,
		"user_id":       "u7",
		// A response carrying its own client identity: the store's must win,
		// which is what the injection before storing is for.
		"client_id":     "server-id",
		"client_secret": "server-secret",
	}, http.StatusOK)
	c := newClientFor(t, srv.URL)
	s, path := newBoundStore(t)
	s.StoreLoginResponse(tokenResponse(map[string]any{"refresh_token": "rt-1"}))

	if err := s.Refresh(context.Background(), c); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if !cap.has("grant_type", "refresh_token") {
		t.Errorf("query %q misses grant_type=refresh_token", cap.rawQuery)
	}
	if !cap.has("refresh_token", "rt-1") {
		t.Errorf("query %q misses refresh_token=rt-1", cap.rawQuery)
	}
	if !cap.has("client_id", config.DefaultClientID) {
		t.Errorf("query %q misses default client_id", cap.rawQuery)
	}
	if cap.has("without_new_session", "1") {
		t.Errorf("query %q: newSession=true must NOT send without_new_session", cap.rawQuery)
	}
	if got := s.AuthorizationValue(); got != "Bearer at-2" {
		t.Errorf("AuthorizationValue() = %q, want the refreshed token", got)
	}
	if got := s.ClientID(); got != config.DefaultClientID {
		t.Errorf("ClientID() = %q, want the store's identity to survive the response's", got)
	}
	if got := s.ClientSecret(); got != config.DefaultClientSecret {
		t.Errorf("ClientSecret() = %q, want the store's identity to survive the response's", got)
	}
	// Refresh must not persist anything implicitly: the caller decides when to
	// save, because a refresh that succeeds in memory and then fails to write is
	// a different outcome from one that never happened.
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Refresh wrote %q, want no implicit save", path)
	}
}

// TestRefreshEmptyRefreshTokenError: with no refresh token stored there is
// nothing to refresh with, so Refresh errors out instead of sending an empty
// parameter.
func TestRefreshEmptyRefreshTokenError(t *testing.T) {
	srv, _ := refreshServer(t, map[string]any{"access_token": "at"}, http.StatusOK)
	c := newClientFor(t, srv.URL)

	if err := newTestStore(t).Refresh(context.Background(), c); err == nil {
		t.Fatal("Refresh: want error for missing refresh token")
	}
}

// TestRefreshNonEmptyJSONWithoutAccessTokenSucceeds: success only requires a
// non-empty JSON object response; no access_token completeness check is added.
func TestRefreshNonEmptyJSONWithoutAccessTokenSucceeds(t *testing.T) {
	srv, _ := refreshServer(t, map[string]any{"weird": "but-nonempty"}, http.StatusOK)
	c := newClientFor(t, srv.URL)
	s := testStore(t, tokenResponse())

	if err := s.Refresh(context.Background(), c); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if s.Empty() {
		t.Error("Empty() = true, want the response to have been stored")
	}
}

func TestRefreshHTTPError(t *testing.T) {
	srv, _ := refreshServer(t, nil, http.StatusInternalServerError)
	c := newClientFor(t, srv.URL)

	if err := testStore(t, tokenResponse()).Refresh(context.Background(), c); err == nil {
		t.Fatal("Refresh: want error on HTTP 500")
	}
}

func TestRefreshNonObjectBodyError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`["not","an","object"]`))
	}))
	defer srv.Close()
	c := newClientFor(t, srv.URL)

	if err := testStore(t, tokenResponse()).Refresh(context.Background(), c); err == nil {
		t.Fatal("Refresh: want error for non-object response")
	}
}

// TestRefreshWithoutNewSessionVariant exercises the private core with
// newSession=false: the without_new_session=1 parameter must appear.
func TestRefreshWithoutNewSessionVariant(t *testing.T) {
	srv, cap := refreshServer(t, map[string]any{"access_token": "at-2", "refresh_token": "rt-2"}, http.StatusOK)
	c := newClientFor(t, srv.URL)
	s := testStore(t, tokenResponse())

	if err := c.refresh(context.Background(), s, false); err != nil {
		t.Fatalf("refresh(false): %v", err)
	}
	if !cap.has("without_new_session", "1") {
		t.Errorf("query %q misses without_new_session=1", cap.rawQuery)
	}
}

// TestRefreshDecodeObjectBoundaryAndStrictness locks the boundary, strictness, and
// object contract of decodeObject in the refresh token flow.
func TestRefreshDecodeObjectBoundaryAndStrictness(t *testing.T) {
	t.Run("protocol boundary and whitespace", func(t *testing.T) {
		boundaryCases := []struct {
			name         string
			body         string
			wantEmptyErr bool
		}{
			{"empty string", "", true},
			{"JSON whitespace", "   \t\r\n ", false},
			{"NBSP only", "\u00a0", false},
			{"NBSP prefixed object", "\u00a0{}", false},
		}
		for _, tc := range boundaryCases {
			t.Run(tc.name, func(t *testing.T) {
				_, err := decodeObject(tc.body)
				if err == nil {
					t.Fatalf("decodeObject(%s) must fail", tc.name)
				}
				hasEmpty := strings.Contains(err.Error(), "empty JSON response")
				if tc.wantEmptyErr && !hasEmpty {
					t.Errorf("decodeObject(%s) err = %v, want 'empty JSON response'", tc.name, err)
				}
				if !tc.wantEmptyErr && hasEmpty {
					t.Errorf("decodeObject(%s) err = %v, must not be classified as 'empty JSON response'", tc.name, err)
				}
			})
		}
	})

	t.Run("v2 syntax strictness", func(t *testing.T) {
		strictCases := []struct {
			name string
			body string
		}{
			{"duplicate keys", `{"a":1,"a":2}`},
			{"invalid UTF-8", "{\"a\":\"\xff\"}"},
		}
		for _, tc := range strictCases {
			t.Run(tc.name, func(t *testing.T) {
				_, err := decodeObject(tc.body)
				if err == nil {
					t.Errorf("decodeObject(%s) must fail", tc.name)
				}
			})
		}
	})

	t.Run("object contract", func(t *testing.T) {
		// Empty object is valid and produces a non-nil empty map
		obj, err := decodeObject(`{}`)
		if err != nil {
			t.Fatalf("decodeObject({}) = %v, want success", err)
		}
		if obj == nil {
			t.Fatal("decodeObject({}) = nil, want non-nil map")
		}

		// null is rejected with the specific object contract error
		_, err = decodeObject(`null`)
		if err == nil {
			t.Fatal("decodeObject(null) must fail")
		}
		if err.Error() != "JSON response was not an object" {
			t.Errorf("decodeObject(null) err = %v, want 'JSON response was not an object'", err)
		}

		// Array is rejected as not an object
		_, err = decodeObject(`[1, 2]`)
		if err == nil {
			t.Fatal("decodeObject([1, 2]) must fail")
		}
	})
}
