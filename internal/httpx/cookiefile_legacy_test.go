package httpx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// legacyCookieFileName is the cookie file an earlier build wrote: Netscape
// columns at the configuration root. Nothing reads it now, and this round
// exists to make that true rather than merely intended, so the name appears
// only in this file.
const legacyCookieFileName = "cookies.txt"

// legacyCookiePath is where an earlier build kept the cookie file: beside the
// one this build writes.
func legacyCookiePath(newPath string) string {
	return filepath.Join(filepath.Dir(newPath), legacyCookieFileName)
}

// writeLegacyCookieFile plants the file an earlier build wrote, in the shape it
// wrote it. It is the one place a test spells out the old text format, because
// that format is what the break is about.
func writeLegacyCookieFile(t *testing.T, path string, rows ...string) {
	t.Helper()
	data := "# Netscape HTTP Cookie File\n" + strings.Join(rows, "\n") + "\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatalf("write legacy cookie file: %v", err)
	}
}

// TestALegacyCookieFileIsNeverRead is the round's central claim about cookies,
// in the shapes that could otherwise pass by accident. The planted rows are
// well-formed and unexpired: if the outcome were "no cookies because the rows
// were bad", these cases would prove nothing.
func TestALegacyCookieFileIsNeverRead(t *testing.T) {
	const legacyValue = "LEGACY-COOKIE-SENTINEL-7d2b"

	t.Run("only the old file: nothing is loaded", func(t *testing.T) {
		path := cookieFileFixturePath(t)
		writeLegacyCookieFile(t, legacyCookiePath(path), "gog.com\tTRUE\t/\tFALSE\t0\tSID\t"+legacyValue)

		c := cookieFileClient(t, path)
		if err := c.LoadCookies(); err != nil {
			t.Fatalf("LoadCookies: %v", err)
		}
		if n := len(c.store.persist); n != 0 {
			t.Errorf("persist size = %d, want 0: the legacy file must contribute nothing", n)
		}
		if cs := c.store.Cookies(mustParse(t, "https://gog.com/")); len(cs) != 0 {
			t.Errorf("cookies = %+v, want none", cs)
		}
	})

	t.Run("a valid old file and no new file: no request carries it", func(t *testing.T) {
		var mu sync.Mutex
		var sent []string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			sent = append(sent, r.Header.Get("Cookie"))
			mu.Unlock()
		}))
		t.Cleanup(srv.Close)

		path := cookieFileFixturePath(t)
		// The row is aimed at the host the request goes to, so a reader of the
		// old format would send it: the absence below is about the file not
		// being read, not about the cookie not matching.
		host := mustParse(t, srv.URL).Hostname()
		writeLegacyCookieFile(t, legacyCookiePath(path), host+"\tFALSE\t/\tFALSE\t0\tSID\t"+legacyValue)

		c := cookieFileClient(t, path)
		if err := c.LoadCookies(); err != nil {
			t.Fatalf("LoadCookies: %v", err)
		}
		if _, err := c.Get(context.Background(), srv.URL+"/"); err != nil {
			t.Fatalf("Get: %v", err)
		}

		mu.Lock()
		defer mu.Unlock()
		if len(sent) != 1 {
			t.Fatalf("the fixture served %d requests, want 1: the guard would be vacuous", len(sent))
		}
		if strings.Contains(sent[0], legacyValue) {
			t.Errorf("the request carried the legacy cookie: %q", sent[0])
		}
	})

	t.Run("both present: only the new file counts", func(t *testing.T) {
		path := cookieFileFixturePath(t)
		writeLegacyCookieFile(t, legacyCookiePath(path), "gog.com\tTRUE\t/\tFALSE\t0\tSID\t"+legacyValue)
		writeCookieFixture(t, path, domainCookie("gog.com", "/", "SID", "current-value"))

		c := cookieFileClient(t, path)
		if err := c.LoadCookies(); err != nil {
			t.Fatalf("LoadCookies: %v", err)
		}
		cs := c.store.Cookies(mustParse(t, "https://gog.com/"))
		if len(cs) != 1 {
			t.Fatalf("cookies = %+v, want the one cookie of the new file", cs)
		}
		if cs[0].Value != "current-value" {
			t.Errorf("value = %q, want the new file's value, never the legacy one", cs[0].Value)
		}
	})

	t.Run("a corrupt old file does not disturb the new one", func(t *testing.T) {
		path := cookieFileFixturePath(t)
		if err := os.WriteFile(legacyCookiePath(path), []byte("# not a cookie file at all\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		writeCookieFixture(t, path, domainCookie("gog.com", "/", "SID", "current-value"))

		c := cookieFileClient(t, path)
		if err := c.LoadCookies(); err != nil {
			t.Fatalf("LoadCookies: %v", err)
		}
		cs := c.store.Cookies(mustParse(t, "https://gog.com/"))
		if len(cs) != 1 || cs[0].Value != "current-value" {
			t.Errorf("cookies = %+v, want the new file used regardless of the old one", cs)
		}
	})
}
