package galaxy

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nekrozis/goggo/internal/auth"
	"github.com/nekrozis/goggo/internal/httpx"
)

// newEmptyStore returns an empty credential store: these tests exercise the
// request the client builds, not the credentials behind it.
func newEmptyStore(t *testing.T) *auth.Store {
	t.Helper()
	s, err := auth.Open("")
	if err != nil {
		t.Fatalf("auth.Open: %v", err)
	}
	return s
}

// newTestClient wires a Client to srv with the token state the test needs.
// Overriding the unexported endpoints block is the same technique the webapi
// tests use, so both flows are pointed at a test server the same way.
func newTestClient(t *testing.T, srv *httptest.Server, token map[string]any) *Client {
	t.Helper()
	hx, err := httpx.New(httpx.Config{
		UserAgent:   "goggo-test/1.0",
		RetryPolicy: httpx.RetryPolicy{MaxAttempts: 1, ShouldRetry: httpx.DefaultShouldRetry},
	})
	if err != nil {
		t.Fatalf("httpx.New: %v", err)
	}
	store := newEmptyStore(t)
	if token != nil {
		store.StoreLoginResponse(token)
	}
	cl, err := New(hx, store)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Both hosts point at the test server: leaving cdn empty would build a
	// RELATIVE manifest URL, and the request would fail on an unsupported
	// scheme (or reach the real CDN).
	cl.ep = endpoints{contentSystem: srv.URL, cdn: srv.URL}
	return cl
}

func TestNewRejectsNilArguments(t *testing.T) {
	hx, err := httpx.New(httpx.Config{})
	if err != nil {
		t.Fatalf("httpx.New: %v", err)
	}
	if _, err := New(nil, newEmptyStore(t)); err == nil {
		t.Error("a nil http client must be an error")
	}
	if _, err := New(hx, nil); err == nil {
		t.Error("a nil credential source must be an error")
	}
}

// TestAuthorizationThreeStates locks the bearer rule: an EXPIRED store
// contributes no header at all, and neither does an unexpired but EMPTY token —
// the request then goes out unauthenticated instead of carrying a malformed
// header. The three states are built through the public token store, so no test
// seam is involved.
func TestAuthorizationThreeStates(t *testing.T) {
	cases := []struct {
		name  string
		token map[string]any
		want  string
	}{
		{"valid token", map[string]any{"access_token": "tok", "expires_in": 3600}, "Bearer tok"},
		{"expired token", map[string]any{"access_token": "tok", "expires_in": -10}, ""},
		{"unexpired but empty", map[string]any{"expires_in": 3600}, ""},
		{"no token at all", nil, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var value string
			var present bool
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				value = r.Header.Get("Authorization")
				_, present = r.Header["Authorization"]
				fmt.Fprint(w, `{"ok":true}`)
			}))
			defer srv.Close()

			cl := newTestClient(t, srv, c.token)
			if _, err := cl.getResponse(context.Background(), srv.URL); err != nil {
				t.Fatalf("getResponse: %v", err)
			}
			if value != c.want {
				t.Errorf("Authorization = %q, want %q", value, c.want)
			}
			if present != (c.want != "") {
				t.Errorf("header present = %v, want %v", present, c.want != "")
			}
		})
	}
}

// TestGetResponseHonoursTransportPolicy: galaxy does not retry on its own, so
// the retry decision must come from the transport's policy.
func TestGetResponseHonoursTransportPolicy(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer srv.Close()

	hx, err := httpx.New(httpx.Config{
		UserAgent:   "goggo-test/1.0",
		RetryPolicy: httpx.RetryPolicy{MaxAttempts: 3, ShouldRetry: httpx.DefaultShouldRetry},
	})
	if err != nil {
		t.Fatalf("httpx.New: %v", err)
	}
	cl, err := New(hx, newEmptyStore(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cl.ep = endpoints{contentSystem: srv.URL, cdn: srv.URL}

	if _, err := cl.getResponseJSON(context.Background(), srv.URL); err != nil {
		t.Fatalf("getResponseJSON: %v", err)
	}
	if calls != 2 {
		t.Errorf("attempts = %d, want 2 (the policy, not galaxy, retries)", calls)
	}
}

// TestGzipBodyIsDecodedTransparently locks the encoding decision: the package
// does not set Accept-Encoding, so the transport negotiates gzip and
// decompresses it. Setting that header here would switch the transparent
// decompression off, and this test is what would fail.
func TestGzipBodyIsDecodedTransparently(t *testing.T) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write([]byte(`{"generation":2}`)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write(buf.Bytes())
	}))
	defer srv.Close()

	cl := newTestClient(t, srv, nil)
	got, err := cl.getResponseJSON(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("getResponseJSON: %v", err)
	}
	if mustInt(t, got["generation"]) != 2 {
		t.Errorf("generation = %#v, want 2", got["generation"])
	}
}

// TestDecodeJSONObjectBoundary locks the single-entry shape contract: a JSON
// OBJECT is the only success, so a top-level array is a failure rather than a
// success carrying the wrong type.
func TestDecodeJSONObjectBoundary(t *testing.T) {
	for _, body := range []string{`{"a":1}`, `  {"a":1}  `, `{"a":{"b":[]}}`} {
		if _, err := decodeJSONObject(body); err != nil {
			t.Errorf("decodeJSONObject(%q) = %v, want success", body, err)
		}
	}
	for _, body := range []string{"", "   ", "{not json", `[1,2]`, `"text"`, "42", "null"} {
		_, err := decodeJSONObject(body)
		if err == nil {
			t.Errorf("decodeJSONObject(%q) must fail", body)
			continue
		}
		if !errors.Is(err, ErrNotJSON) {
			t.Errorf("decodeJSONObject(%q) = %v, want ErrNotJSON", body, err)
		}
	}
}

// zlibBody returns plain compressed as a zlib stream.
func zlibBody(t *testing.T, plain string) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	if _, err := zw.Write([]byte(plain)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// TestZlibFallback locks the compressed-body path of
// just as importantly, what it does NOT do: inflation is attempted only after a
// failed decode AND only for a body starting with a zlib stream header, so
// ordinary malformed JSON keeps its single ErrNotJSON.
func TestZlibFallback(t *testing.T) {
	compressed := zlibBody(t, `{"items":[]}`)
	// The header check is written byte-wise, like the production code: a
	// "\x9c" escape in a string literal is a BYTE, so comparing it as a rune
	// would go through UTF-8 decoding and never match (0x9c is not valid UTF-8
	// on its own).
	if len(compressed) < 2 || compressed[0] != 0x78 {
		t.Fatalf("fixture is not a recognised zlib stream: % x", compressed[:2])
	}
	switch compressed[1] {
	case 0x01, 0x5e, 0x9c, 0xda:
	default:
		t.Fatalf("fixture is not a recognised zlib stream: % x", compressed[:2])
	}
	got, err := decodeJSONObject(compressed)
	if err != nil {
		t.Fatalf("decodeJSONObject(zlib body) = %v, want success", err)
	}
	if _, ok := got["items"]; !ok {
		t.Errorf("decoded object = %v, want the items member", got)
	}

	// Not JSON, and no zlib header: never inflated.
	if _, err := decodeJSONObject("plainly not json"); !errors.Is(err, ErrNotJSON) {
		t.Errorf("non-zlib body = %v, want ErrNotJSON", err)
	}
	// A zlib header over bytes that are not a zlib stream: the retry fails and
	// the first error stands; no separate inflation error is surfaced.
	if _, err := decodeJSONObject("\x78\x9cgarbage"); !errors.Is(err, ErrNotJSON) {
		t.Errorf("bogus zlib stream = %v, want ErrNotJSON", err)
	}
	// A zlib stream whose contents are still not a JSON object: also ErrNotJSON.
	if _, err := decodeJSONObject(zlibBody(t, `[1,2]`)); !errors.Is(err, ErrNotJSON) {
		t.Errorf("zlib of a non-object = %v, want ErrNotJSON", err)
	}
}

// TestHTTPErrorIsStatusError: an HTTP failure keeps the httpx error semantics
// (galaxy does not build its own), and the message carries no token even though
// the request did.
func TestHTTPErrorIsStatusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cl := newTestClient(t, srv, map[string]any{"access_token": "super-secret", "expires_in": 3600})
	_, err := cl.getResponse(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("getResponse must report the HTTP failure")
	}
	var se *httpx.StatusError
	if !errors.As(err, &se) || se.Code != http.StatusInternalServerError {
		t.Fatalf("err = %v, want *httpx.StatusError with code 500", err)
	}
	if strings.Contains(err.Error(), "super-secret") {
		t.Errorf("err = %q must not carry the access token", err)
	}
}
