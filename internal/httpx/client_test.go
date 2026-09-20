package httpx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testConfig() Config {
	return Config{
		UserAgent: "goggo-test/1.0",
		Timeout:   5 * time.Second,
	}
}

// TestDoBytesWithRetrySendsCallerRequestEveryAttempt locks the contract of
// DoBytesWithRetry: the caller's own request — and therefore its headers — is
// sent on every attempt, and the request path is not bypassed, so the
// configured User-Agent still arrives. The Galaxy content endpoints rely on
// exactly this: a Bearer header of their own, plus the transport's policy.
func TestDoBytesWithRetrySendsCallerRequestEveryAttempt(t *testing.T) {
	var calls, lostAuth, lostUA int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if r.Header.Get("Authorization") != "Bearer tok" {
			atomic.AddInt32(&lostAuth, 1)
		}
		if r.UserAgent() != "goggo-test/1.0" {
			atomic.AddInt32(&lostUA, 1)
		}
		if n < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer srv.Close()

	cfg := testConfig()
	cfg.RetryPolicy = DefaultPolicy(3, time.Millisecond)
	c, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer tok")

	body, err := c.DoBytesWithRetry(context.Background(), req)
	if err != nil {
		t.Fatalf("DoBytesWithRetry: %v", err)
	}
	if string(body) != `{"ok":true}` {
		t.Errorf("body = %q", body)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("attempts = %d, want 3", got)
	}
	if got := atomic.LoadInt32(&lostAuth); got != 0 {
		t.Errorf("%d attempt(s) lost the caller's Authorization header", got)
	}
	if got := atomic.LoadInt32(&lostUA); got != 0 {
		t.Errorf("%d attempt(s) bypassed the configured User-Agent", got)
	}
}

// TestDoBytesWithRetryExhaustedStatus locks the error face: a retryable status
// that survives the policy becomes a *StatusError naming the request's method
// and URL, and the error text carries no header value.
func TestDoBytesWithRetryExhaustedStatus(t *testing.T) {
	srv, calls := sequenceServer(t, http.StatusInternalServerError)

	cfg := testConfig()
	cfg.RetryPolicy = DefaultPolicy(2, time.Millisecond)
	c, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer secret-token")

	_, err = c.DoBytesWithRetry(context.Background(), req)
	if err == nil {
		t.Fatal("DoBytesWithRetry must report the exhausted status")
	}
	var se *StatusError
	if !errors.As(err, &se) {
		t.Fatalf("err = %v, want *StatusError", err)
	}
	if se.Code != http.StatusInternalServerError || se.Method != http.MethodGet || se.URL != srv.URL {
		t.Errorf("StatusError = %+v, want %s %s: HTTP 500", se, http.MethodGet, srv.URL)
	}
	if got := atomic.LoadInt32(calls); got != 2 {
		t.Errorf("attempts = %d, want 2", got)
	}
	if strings.Contains(err.Error(), "secret-token") {
		t.Errorf("err = %q must not carry the Authorization header value", err)
	}
}

// TestDoBytesWithRetryNilRequest: a nil request is reported, not a panic.
func TestDoBytesWithRetryNilRequest(t *testing.T) {
	c, err := New(testConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c.DoBytesWithRetry(context.Background(), nil); err == nil {
		t.Error("a nil request must be an error")
	}
}
func TestDoSendsUserAgent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.UserAgent() != "goggo-test/1.0" {
			t.Errorf("User-Agent = %q", r.UserAgent())
		}
		fmt.Fprint(w, "hello")
	}))
	defer srv.Close()

	c, err := New(testConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := c.Do(context.Background(), req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "hello" {
		t.Errorf("body = %q", body)
	}
}

func TestGetReturnsRawResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "raw")
	}))
	defer srv.Close()

	c, err := New(testConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resp, err := c.Get(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "raw" {
		t.Errorf("body = %q", body)
	}
}

func TestGetBytesFollowsRedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/final", http.StatusFound)
			return
		}
		fmt.Fprint(w, "final-body")
	}))
	defer srv.Close()

	c, _ := New(testConfig())
	body, err := c.GetBytes(context.Background(), srv.URL+"/start")
	if err != nil {
		t.Fatalf("GetBytes: %v", err)
	}
	if string(body) != "final-body" {
		t.Errorf("body = %q", body)
	}
}

// TestDoNoRedirectReturns3xx locks the per-call no-follow behaviour: a 302 is
// returned as-is with its Location header, while a follow-up Do on the same
// Client still follows redirects (no client-wide mode switch).
func TestDoNoRedirectReturns3xx(t *testing.T) {
	var redirects int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			redirects++
			http.Redirect(w, r, "/final", http.StatusFound)
			return
		}
		fmt.Fprint(w, "final-body")
	}))
	defer srv.Close()

	c, _ := New(testConfig())

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/start", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.DoNoRedirect(context.Background(), req)
	if err != nil {
		t.Fatalf("DoNoRedirect: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302 (redirect must not be followed)", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/final" {
		t.Errorf("Location = %q, want /final", loc)
	}

	// Same client, normal path: default redirect following is untouched.
	body, err := c.GetBytes(context.Background(), srv.URL+"/start")
	if err != nil {
		t.Fatalf("GetBytes after DoNoRedirect: %v", err)
	}
	if string(body) != "final-body" {
		t.Errorf("body = %q", body)
	}
	if redirects != 2 {
		t.Errorf("redirect hits = %d, want 2 (one per request)", redirects)
	}
}

func TestGetBytesStatusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c, _ := New(testConfig())
	_, err := c.GetBytes(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected StatusError")
	}
	se, ok := err.(*StatusError)
	if !ok {
		t.Fatalf("error = %T, want *StatusError", err)
	}
	if se.Code != http.StatusInternalServerError {
		t.Errorf("code = %d", se.Code)
	}
	if !strings.Contains(err.Error(), "HTTP 500") {
		t.Errorf("error message %q misses HTTP 500", err.Error())
	}
}

func TestStatusError404Probes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c, _ := New(testConfig())
	_, err := c.GetBytes(context.Background(), srv.URL)
	if !IsNotFound(err) {
		t.Errorf("IsNotFound(%v) = false", err)
	}
	if IsForbidden(err) {
		t.Errorf("IsForbidden(%v) = true", err)
	}
}

func TestGetInsecureSkipVerify(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "tls-ok")
	}))
	defer srv.Close()

	cfg := testConfig()
	cfg.InsecureSkipVerify = true // certificate verification off
	c, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	body, err := c.GetBytes(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("GetBytes against self-signed server: %v", err)
	}
	if string(body) != "tls-ok" {
		t.Errorf("body = %q", body)
	}
}

// TestNewCookieFileWiring covers the four Config combinations: CookieFile is
// wired to a cookieStore only when this package builds the http.Client, and a
// caller-provided HTTPClient is never modified.
func TestNewCookieFileWiring(t *testing.T) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	caller := &http.Client{Jar: jar}
	path := filepath.Join(t.TempDir(), "cookies.txt")

	cases := []struct {
		name       string
		cfg        Config
		wantStore  bool
		wantFile   string
		wantShared bool
	}{
		{"default", Config{}, false, "", false},
		{"cookie file", Config{CookieFile: path}, true, path, false},
		{"caller client", Config{HTTPClient: caller}, false, "", true},
		{"caller client + cookie file", Config{HTTPClient: caller, CookieFile: path}, false, path, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := New(tc.cfg)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if got := c.store != nil; got != tc.wantStore {
				t.Errorf("store != nil = %v, want %v", got, tc.wantStore)
			}
			if c.cookieFile != tc.wantFile {
				t.Errorf("cookieFile = %q, want %q", c.cookieFile, tc.wantFile)
			}
			if tc.wantShared {
				if c.hc != caller {
					t.Error("caller-provided HTTPClient was not used as-is")
				}
				if c.hc.Jar != jar {
					t.Error("caller-provided jar was replaced")
				}
			} else if c.hc.Jar == nil {
				t.Error("transport has no cookie jar")
			} else if tc.wantStore && c.hc.Jar != http.CookieJar(c.store) {
				t.Error("store is not installed as the transport jar")
			}
		})
	}
}

// rewriteTransport is the shape a caller uses to point the production hosts at a
// local server, and it records the hosts it saw.
type rewriteTransport struct {
	target *url.URL
	hosts  []string
}

func (t *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.hosts = append(t.hosts, req.URL.Host)
	clone := req.Clone(req.Context())
	u := *req.URL
	u.Scheme, u.Host = t.target.Scheme, t.target.Host
	clone.URL = &u
	return http.DefaultTransport.RoundTrip(clone)
}

// TestTransportOverrideKeepsTheCookieJar locks that replacing the network exit
// must leave the client, its jar and the cookie file exactly what this package
// builds. A caller-provided HTTPClient cannot do that — it decides the jar, which
// is why it is refused together with a CookieFile.
func TestTransportOverrideKeepsTheCookieJar(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/set":
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "abc", Path: "/"})
		default:
			fmt.Fprint(w, "ok")
		}
	}))
	defer srv.Close()

	target, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	exit := &rewriteTransport{target: target}
	cookieFile := filepath.Join(t.TempDir(), "cookies.txt")

	c, err := New(Config{CookieFile: cookieFile, UserAgent: "goggo-test/1.0", Transport: exit})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c.Get(context.Background(), "https://www.gog.com/set"); err != nil {
		t.Fatalf("Get through the replacement: %v", err)
	}
	if len(exit.hosts) != 1 || exit.hosts[0] != "www.gog.com" {
		t.Errorf("the replacement saw hosts %v, want the production host", exit.hosts)
	}

	// The jar is still this package's, so persistence is untouched: the same
	// client saves, and a fresh one loads.
	saved, err := c.SaveCookies()
	if err != nil {
		t.Fatalf("SaveCookies with a replaced transport: %v", err)
	}
	if saved.Written == 0 {
		t.Error("SaveCookies wrote no rows, want the cookie the jar received")
	}
	fresh, err := New(Config{CookieFile: cookieFile, Transport: exit})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := fresh.LoadCookies(); err != nil {
		t.Fatalf("LoadCookies with a replaced transport: %v", err)
	}
}
