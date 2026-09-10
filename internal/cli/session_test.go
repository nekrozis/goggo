package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/nekrozis/goggo/internal/httpx"
)

// TestTransportOwnedByCallerPersistsCookies locks the S12-R ownership rule: the
// transport is created (and therefore owned) by the caller, and the jar that
// carried a session cookie is the one SaveCookies writes and LoadCookies
// restores.
//
// Coverage boundary (recorded in the audit): the cookie is set by a direct
// request rather than "during a webapi call", because webapi's endpoints have
// no injection point. The property being tested — one transport, one jar, one
// persistence path — is what the ownership change guarantees; the end-to-end
// form is verified by GATE-A.
func TestTransportOwnedByCallerPersistsCookies(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/set":
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "abc", Path: "/"})
		default:
			if _, err := r.Cookie("SID"); err == nil {
				fmt.Fprint(w, "with-cookie")
				return
			}
			fmt.Fprint(w, "no-cookie")
		}
	}))
	defer srv.Close()

	cookieFile := filepath.Join(t.TempDir(), "cookies.txt")

	// First transport: receives the cookie, then persists the jar.
	first, err := httpx.New(httpx.Config{CookieFile: cookieFile})
	if err != nil {
		t.Fatalf("httpx.New: %v", err)
	}
	if _, err := first.Get(context.Background(), srv.URL+"/set"); err != nil {
		t.Fatalf("Get(/set): %v", err)
	}
	if _, err := first.SaveCookies(); err != nil {
		t.Fatalf("SaveCookies: %v", err)
	}

	// Second transport: fresh, empty jar restored from the same file.
	second, err := httpx.New(httpx.Config{CookieFile: cookieFile})
	if err != nil {
		t.Fatalf("httpx.New: %v", err)
	}
	if err := second.LoadCookies(); err != nil {
		t.Fatalf("LoadCookies: %v", err)
	}
	resp, err := second.Get(context.Background(), srv.URL+"/check")
	if err != nil {
		t.Fatalf("Get(/check): %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != "with-cookie" {
		t.Errorf("body = %q, want the cookie to be sent by the restored jar", body)
	}
}
