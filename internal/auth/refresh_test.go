package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestRefreshSuccess(t *testing.T) {
	srv, cap := refreshServer(t, map[string]any{
		"access_token":  "at-2",
		"refresh_token": "rt-2",
		"expires_in":    3600,
		"user_id":       "u7",
	}, http.StatusOK)
	c := newClientFor(t, srv.URL)
	g := testGalaxy(t, tokenMap(map[string]any{"refresh_token": "rt-1"}))

	if err := c.Refresh(context.Background(), g); err != nil {
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
	if got := g.GetAccessToken(); got != "at-2" {
		t.Errorf("access token = %q, want at-2", got)
	}
	if got := g.GetJSON()["client_id"]; got != config.DefaultClientID {
		t.Errorf("stored client_id = %v, want injected default", got)
	}
	if got := g.GetJSON()["client_secret"]; got != config.DefaultClientSecret {
		t.Errorf("stored client_secret = %v, want injected default", got)
	}
	// Refresh must not persist anything implicitly.
	if got := g.GetFilepath(); got != "" {
		t.Errorf("unexpected filepath set: %q", got)
	}
}

// TestRefreshEmptyRefreshTokenError: with no refresh token stored there is
// nothing to refresh with; C++ would send an empty parameter, Go errors out.
func TestRefreshEmptyRefreshTokenError(t *testing.T) {
	srv, _ := refreshServer(t, map[string]any{"access_token": "at"}, http.StatusOK)
	c := newClientFor(t, srv.URL)
	g := config.NewGalaxyConfig() // empty store

	if err := c.Refresh(context.Background(), g); err == nil {
		t.Fatal("Refresh: want error for missing refresh token")
	}
}

// TestRefreshNonEmptyJSONWithoutAccessTokenSucceeds locks the C++ success
// semantics: refreshLogin only tests that the response JSON is non-empty
// (galaxyapi.cpp:67-70); no access_token completeness check is added.
func TestRefreshNonEmptyJSONWithoutAccessTokenSucceeds(t *testing.T) {
	srv, _ := refreshServer(t, map[string]any{"weird": "but-nonempty"}, http.StatusOK)
	c := newClientFor(t, srv.URL)
	g := testGalaxy(t, tokenMap())

	if err := c.Refresh(context.Background(), g); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if got := g.GetJSON()["weird"]; got != "but-nonempty" {
		t.Errorf("stored weird field = %v", got)
	}
}

func TestRefreshHTTPError(t *testing.T) {
	srv, _ := refreshServer(t, nil, http.StatusInternalServerError)
	c := newClientFor(t, srv.URL)
	g := testGalaxy(t, tokenMap())

	if err := c.Refresh(context.Background(), g); err == nil {
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
	g := testGalaxy(t, tokenMap())

	if err := c.Refresh(context.Background(), g); err == nil {
		t.Fatal("Refresh: want error for non-object response")
	}
}

// TestRefreshWithoutNewSessionVariant exercises the private core with
// newSession=false: the without_new_session=1 parameter must appear.
func TestRefreshWithoutNewSessionVariant(t *testing.T) {
	srv, cap := refreshServer(t, map[string]any{"access_token": "at-2", "refresh_token": "rt-2"}, http.StatusOK)
	c := newClientFor(t, srv.URL)
	g := testGalaxy(t, tokenMap())

	if err := c.refreshSession(context.Background(), g, false); err != nil {
		t.Fatalf("refreshSession(false): %v", err)
	}
	if !cap.has("without_new_session", "1") {
		t.Errorf("query %q misses without_new_session=1", cap.rawQuery)
	}
}
