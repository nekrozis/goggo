package httpx

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testConfig() Config {
	return Config{
		UserAgent: "goggo-test/1.0",
		Timeout:   5 * time.Second,
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
	cfg.InsecureSkipVerify = true // mirrors CURLOPT_SSL_VERIFYPEER off
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
