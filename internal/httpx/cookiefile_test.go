package httpx

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nekrozis/goggo/internal/cookiefile"
)

// cookieFileClient builds a Client whose jar is a cookieStore, the way a
// caller enables cookie persistence.
func cookieFileClient(t *testing.T, path string) *Client {
	t.Helper()
	c, err := New(Config{CookieFile: path})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.store == nil {
		t.Fatal("New: store is nil although CookieFile is set")
	}
	return c
}

// writeFixture writes a minimal cookies.txt with the given rows.
func writeFixture(t *testing.T, path string, rows ...string) {
	t.Helper()
	data := "# Netscape HTTP Cookie File\n" + strings.Join(rows, "\n") + "\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %q: %v", path, err)
	}
	return data
}

// cookiePairs renders sent cookies as a sorted multiset of name=value. The
// jar's own ordering is (path length desc, per-store sequence number), so a
// reload can legitimately reorder same-path cookies; comparing the sorted set
// is the stable form of "the same cookies are sent".
func cookiePairs(cs []*http.Cookie) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.Name+"="+c.Value)
	}
	sort.Strings(out)
	return out
}

func TestLoadCookiesUnconfiguredIsNoop(t *testing.T) {
	c, err := New(Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.store != nil {
		t.Error("store must be nil without CookieFile")
	}
	if err := c.LoadCookies(); err != nil {
		t.Errorf("LoadCookies without CookieFile = %v, want nil", err)
	}
}

func TestSaveCookiesUnconfiguredFails(t *testing.T) {
	c, err := New(Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c.SaveCookies(); !errors.Is(err, ErrCookieFileNotConfigured) {
		t.Errorf("SaveCookies = %v, want ErrCookieFileNotConfigured", err)
	}
}

func TestCookieFileUnsupportedWithCallerHTTPClient(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies.txt")
	c, err := New(Config{HTTPClient: &http.Client{}, CookieFile: path})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.store != nil {
		t.Error("store must be nil when the caller supplies HTTPClient")
	}
	if err := c.LoadCookies(); !errors.Is(err, ErrCookieFileUnsupported) {
		t.Errorf("LoadCookies = %v, want ErrCookieFileUnsupported", err)
	}
	if _, err := c.SaveCookies(); !errors.Is(err, ErrCookieFileUnsupported) {
		t.Errorf("SaveCookies = %v, want ErrCookieFileUnsupported", err)
	}
}

func TestLoadCookiesMissingFileIsEmptyState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.txt")
	c := cookieFileClient(t, path)
	if err := c.LoadCookies(); err != nil {
		t.Fatalf("LoadCookies: %v", err)
	}
	if cs := c.store.Cookies(mustParse(t, "https://example.com/")); len(cs) != 0 {
		t.Errorf("cookies = %+v, want none", cs)
	}
}

func TestLoadCookiesReadErrorIsWrappedWithPath(t *testing.T) {
	dir := t.TempDir() // reading a directory is an I/O error, not "not exist"
	c := cookieFileClient(t, dir)
	err := c.LoadCookies()
	if err == nil {
		t.Fatal("LoadCookies: want error when the file cannot be read")
	}
	if !strings.Contains(err.Error(), dir) {
		t.Errorf("error %q must carry the path", err)
	}
}

func TestLoadCookiesSkipsMalformedRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies.txt")
	writeFixture(t, path,
		"good.example\tFALSE\t/\tFALSE\t0\tGOOD\t1",
		"too\tfew\tcolumns",
		"bad-flag.example\tMAYBE\t/\tFALSE\t0\tX\t1",
		"# a comment",
	)
	c := cookieFileClient(t, path)
	if err := c.LoadCookies(); err != nil {
		t.Fatalf("LoadCookies: %v", err)
	}
	if cs := c.store.Cookies(mustParse(t, "https://good.example/")); len(cs) != 1 || cs[0].Name != "GOOD" {
		t.Errorf("good row not loaded: %+v", cs)
	}
	if n := len(c.store.persist); n != 1 {
		t.Errorf("persist size = %d, want 1 (malformed rows skipped, not fatal)", n)
	}
}

func TestLoadReconstructsHostOnlyCookie(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies.txt")
	writeFixture(t, path, "www.example.com\tFALSE\t/\tFALSE\t0\tSID\tv1")
	c := cookieFileClient(t, path)
	if err := c.LoadCookies(); err != nil {
		t.Fatalf("LoadCookies: %v", err)
	}

	if cs := c.store.Cookies(mustParse(t, "https://www.example.com/")); len(cs) != 1 {
		t.Errorf("host-only cookie not sent to its own host: %+v", cs)
	}
	if cs := c.store.Cookies(mustParse(t, "https://example.com/")); len(cs) != 0 {
		t.Errorf("host-only cookie must not be sent to the parent domain: %+v", cs)
	}
	if cs := c.store.Cookies(mustParse(t, "https://other.example.com/")); len(cs) != 0 {
		t.Errorf("host-only cookie must not be sent to a sibling host: %+v", cs)
	}
}

func TestLoadReconstructsDomainCookie(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies.txt")
	writeFixture(t, path, ".example.com\tTRUE\t/\tFALSE\t0\tDOM\tv2")
	c := cookieFileClient(t, path)
	if err := c.LoadCookies(); err != nil {
		t.Fatalf("LoadCookies: %v", err)
	}

	for _, raw := range []string{"https://example.com/", "https://foo.example.com/", "https://bar.example.com/deep"} {
		if cs := c.store.Cookies(mustParse(t, raw)); len(cs) != 1 {
			t.Errorf("domain cookie not sent to %s: %+v", raw, cs)
		}
	}
	if cs := c.store.Cookies(mustParse(t, "https://example.org/")); len(cs) != 0 {
		t.Errorf("domain cookie leaked to another domain: %+v", cs)
	}
}

// TestLoadCompletesDefaultPath checks that a row without a path is completed
// with the same RFC default-path helper used when recording events.
func TestLoadCompletesDefaultPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies.txt")
	writeFixture(t, path, "example.com\tFALSE\t\tFALSE\t0\tSID\tv3")
	c := cookieFileClient(t, path)
	if err := c.LoadCookies(); err != nil {
		t.Fatalf("LoadCookies: %v", err)
	}

	key := cookieKey{domain: "example.com", path: "/", name: "SID", hostOnly: true}
	if _, ok := c.store.persist[key]; !ok {
		t.Fatalf("persist key %+v missing (have %v)", key, c.store.persist)
	}
	if cs := c.store.Cookies(mustParse(t, "https://example.com/deep/path")); len(cs) != 1 {
		t.Errorf("cookie with default path / not sent on a deeper path: %+v", cs)
	}
}

func TestLoadReconstructsSecureCookie(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies.txt")
	writeFixture(t, path, "example.com\tFALSE\t/\tTRUE\t0\tSEC\tv4")
	c := cookieFileClient(t, path)
	if err := c.LoadCookies(); err != nil {
		t.Fatalf("LoadCookies: %v", err)
	}

	if cs := c.store.Cookies(mustParse(t, "http://example.com/")); len(cs) != 0 {
		t.Errorf("secure cookie sent over http: %+v", cs)
	}
	if cs := c.store.Cookies(mustParse(t, "https://example.com/")); len(cs) != 1 {
		t.Errorf("secure cookie not sent over https: %+v", cs)
	}
}

func TestLoadPreservesHttpOnlyRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies.txt")
	writeFixture(t, path, "#HttpOnly_example.com\tFALSE\t/\tFALSE\t0\tSID\tv5")
	c := cookieFileClient(t, path)
	if err := c.LoadCookies(); err != nil {
		t.Fatalf("LoadCookies: %v", err)
	}

	key := cookieKey{domain: "example.com", path: "/", name: "SID", hostOnly: true}
	if st, ok := c.store.persist[key]; !ok || !st.httpOnly {
		t.Fatalf("persist = %+v (ok=%v), want httpOnly", st, ok)
	}
	if _, err := c.SaveCookies(); err != nil {
		t.Fatalf("SaveCookies: %v", err)
	}
	if body := string(mustRead(t, path)); !strings.Contains(body, "#HttpOnly_example.com") {
		t.Errorf("saved file lost the #HttpOnly_ prefix:\n%s", body)
	}
}

// TestLoadSessionCookieStaysSession locks D1: expiry=0 means "session cookie",
// which is persisted and reloaded as a session cookie, not as a persisting
// cookie with an invented expiry.
func TestLoadSessionCookieStaysSession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies.txt")
	writeFixture(t, path, "example.com\tFALSE\t/\tFALSE\t0\tSESS\tv6")
	c := cookieFileClient(t, path)
	if err := c.LoadCookies(); err != nil {
		t.Fatalf("LoadCookies: %v", err)
	}

	key := cookieKey{domain: "example.com", path: "/", name: "SESS", hostOnly: true}
	if st, ok := c.store.persist[key]; !ok || !st.expires.IsZero() {
		t.Fatalf("persist = %+v (ok=%v), want session (zero expiry)", st, ok)
	}
	if _, err := c.SaveCookies(); err != nil {
		t.Fatalf("SaveCookies: %v", err)
	}
	rows := cookiefile.Parse(mustRead(t, path))
	if len(rows) != 1 || !rows[0].Expires.IsZero() {
		t.Errorf("session cookie not written with expiry 0: %+v", rows)
	}
}

// TestLoadFeedsEventsThroughRecord proves Load goes through the SetCookies
// event path rather than writing persist directly: an already-expired row is
// refused by record, exactly as a live expired event would be.
func TestLoadFeedsEventsThroughRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies.txt")
	writeFixture(t, path, "example.com\tFALSE\t/\tFALSE\t1\tOLD\tv7") // expiry 1970 -> past
	c := cookieFileClient(t, path)
	if err := c.LoadCookies(); err != nil {
		t.Fatalf("LoadCookies: %v", err)
	}
	if n := len(c.store.persist); n != 0 {
		t.Errorf("persist size = %d, want 0 (expired row must not enter persist)", n)
	}
}

func TestSaveDropsEntriesExpiredSinceRecording(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies.txt")
	c := cookieFileClient(t, path)
	c.store.now = func() time.Time { return fixedNow }

	u := mustParse(t, "https://example.com/")
	c.store.SetCookies(u, []*http.Cookie{{Name: "K", Value: "v", Expires: fixedNow.Add(time.Hour)}})
	if n := len(c.store.persist); n != 1 {
		t.Fatalf("persist size = %d, want 1", n)
	}

	c.store.now = func() time.Time { return fixedNow.Add(2 * time.Hour) }
	res, err := c.SaveCookies()
	if err != nil {
		t.Fatalf("SaveCookies: %v", err)
	}
	if res.Written != 0 {
		t.Errorf("Written = %d, want 0", res.Written)
	}
	if rows := cookiefile.Parse(mustRead(t, path)); len(rows) != 0 {
		t.Errorf("expired cookie was written: %+v", rows)
	}
}

func TestSaveWritesDeterministicSortedOutput(t *testing.T) {
	dir := t.TempDir()
	insert := []struct {
		raw string
		c   *http.Cookie
	}{
		{"https://zeta.example/", &http.Cookie{Name: "B", Value: "2"}},
		{"https://alpha.example/", &http.Cookie{Name: "A", Value: "1"}},
		{"https://mid.example/", &http.Cookie{Name: "C", Value: "3"}},
	}

	save := func(path string, order []int) []byte {
		c := cookieFileClient(t, path)
		c.store.now = func() time.Time { return fixedNow }
		for _, i := range order {
			c.store.SetCookies(mustParse(t, insert[i].raw), []*http.Cookie{insert[i].c})
		}
		if _, err := c.SaveCookies(); err != nil {
			t.Fatalf("SaveCookies: %v", err)
		}
		return mustRead(t, path)
	}

	forward := save(filepath.Join(dir, "fwd.txt"), []int{0, 1, 2})
	reverse := save(filepath.Join(dir, "rev.txt"), []int{2, 1, 0})
	if !bytes.Equal(forward, reverse) {
		t.Errorf("insertion order changed output:\nforward=%q\nreverse=%q", forward, reverse)
	}

	lines := strings.Split(strings.TrimSpace(string(forward)), "\n")
	if len(lines) != 4 {
		t.Fatalf("lines = %d (%q), want header + 3 rows", len(lines), forward)
	}
	if rows := cookiefile.Parse(forward); len(rows) != 3 ||
		rows[0].Domain != "alpha.example" || rows[1].Domain != "mid.example" || rows[2].Domain != "zeta.example" {
		t.Errorf("rows not sorted by domain: %+v", rows)
	}
}

func TestSaveReplacesFileAtomicallyWith0600(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies.txt")
	if err := os.WriteFile(path, []byte("stale garbage without a header\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	c := cookieFileClient(t, path)
	c.store.now = func() time.Time { return fixedNow }
	c.store.SetCookies(mustParse(t, "https://example.com/"), []*http.Cookie{hostCookie("SID", "v")})
	if _, err := c.SaveCookies(); err != nil {
		t.Fatalf("SaveCookies: %v", err)
	}

	data := mustRead(t, path)
	if strings.Contains(string(data), "stale garbage") {
		t.Errorf("previous content survived the replace: %q", data)
	}
	if !strings.HasPrefix(string(data), "# Netscape HTTP Cookie File") {
		t.Errorf("file lacks the codec header: %q", data)
	}
	if entries, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".goggo-cookies-*")); err != nil || len(entries) != 0 {
		t.Errorf("temp file left behind: %v (%v)", entries, err)
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if perm := fi.Mode().Perm(); perm != 0o600 {
			t.Errorf("mode = %o, want 600", perm)
		}
	}
}

// TestRoundTripPreservesSendBehaviourAndBytes is the integration test: state
// saved by one client and loaded by a fresh one must send the same cookies, and
// re-saving must produce identical bytes.
func TestRoundTripPreservesSendBehaviourAndBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies.txt")

	src := cookieFileClient(t, path)
	src.store.now = func() time.Time { return fixedNow }
	u := mustParse(t, "https://www.example.com/account/settings")
	src.store.SetCookies(u, []*http.Cookie{
		{Name: "HOST", Value: "h", Path: "/account"},
		{Name: "DOM", Value: "d", Domain: ".example.com"},
		{Name: "SEC", Value: "s", Secure: true},
		{Name: "SESS", Value: "x"},
		{Name: "KEEP", Value: "k", Expires: fixedNow.Add(24 * time.Hour)},
	})
	if _, err := src.SaveCookies(); err != nil {
		t.Fatalf("SaveCookies: %v", err)
	}
	first := mustRead(t, path)

	dst := cookieFileClient(t, path)
	// Same injected clock as the writer: KEEP expires at fixedNow+24h, which
	// the real wall clock would already treat as expired.
	dst.store.now = func() time.Time { return fixedNow }
	if err := dst.LoadCookies(); err != nil {
		t.Fatalf("LoadCookies: %v", err)
	}

	probes := []string{
		"https://www.example.com/account/settings",
		"https://www.example.com/account",
		"https://www.example.com/",
		"https://www.example.com/elsewhere",
		"https://foo.example.com/",
		"https://example.com/account",
		"http://www.example.com/",
	}
	for _, p := range probes {
		pu := mustParse(t, p)
		from := cookiePairs(src.store.Cookies(pu))
		to := cookiePairs(dst.store.Cookies(pu))
		if !slices.Equal(from, to) {
			t.Errorf("Cookies(%q) diverged after reload:\nsrc=%v\ndst=%v", p, from, to)
		}
	}

	if _, err := dst.SaveCookies(); err != nil {
		t.Fatalf("second SaveCookies: %v", err)
	}
	if second := mustRead(t, path); !bytes.Equal(first, second) {
		t.Errorf("Save->Load->Save is not byte-identical:\nfirst =%q\nsecond=%q", first, second)
	}
}

// TestConcurrentSetCookiesAndSave is a smoke test for the lock discipline:
// Save snapshots under the mutex and does file I/O outside it, so writers and
// savers must not deadlock or panic. (The race detector needs cgo, which this
// project deliberately avoids.)
func TestConcurrentSetCookiesAndSave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies.txt")
	c := cookieFileClient(t, path)
	u := mustParse(t, "https://gog.com/")

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			c.store.SetCookies(u, []*http.Cookie{hostCookie("SID", fmt.Sprint(i))})
		}
	}()
	for i := 0; i < 20; i++ {
		if _, err := c.SaveCookies(); err != nil {
			t.Fatalf("SaveCookies: %v", err)
		}
	}
	wg.Wait()

	res, err := c.SaveCookies()
	if err != nil {
		t.Fatalf("final SaveCookies: %v", err)
	}
	if res.Written != 1 {
		t.Errorf("Written = %d, want 1", res.Written)
	}
	if rows := cookiefile.Parse(mustRead(t, path)); len(rows) != 1 {
		t.Errorf("rows = %+v, want one SID row", rows)
	}
}
