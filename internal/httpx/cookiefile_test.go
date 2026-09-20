package httpx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
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
	"github.com/nekrozis/goggo/internal/secretfile"
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

// cookieFileFixturePath is the file a test writes its fixture to. The name is
// the production one, so a test never has to think about it.
func cookieFileFixturePath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "cookies.bin")
}

// hostOnlyCookie and domainCookie build the stored records the fixtures use.
// They differ only in the flag that says whether the cookie belongs to one host
// or to a whole domain.
func hostOnlyCookie(domain, path, name, value string) cookiefile.PersistentCookie {
	return cookiefile.PersistentCookie{Domain: domain, Path: path, Name: name, Value: value, HostOnly: true}
}

func domainCookie(domain, path, name, value string) cookiefile.PersistentCookie {
	return cookiefile.PersistentCookie{Domain: domain, Path: path, Name: name, Value: value}
}

// writeCookieFixture plants a cookie file through this package's own encoding,
// so a test states the cookies it wants rather than the bytes of the file.
func writeCookieFixture(t *testing.T, path string, cookies ...cookiefile.PersistentCookie) {
	t.Helper()
	if err := os.WriteFile(path, encodeCookieFile(cookies), 0o600); err != nil {
		t.Fatalf("write cookie fixture: %v", err)
	}
}

// readCookieFile decodes a cookie file through this package's own decoding.
func readCookieFile(t *testing.T, path string) []cookiefile.PersistentCookie {
	t.Helper()
	cookies, err := decodeCookieFile(mustRead(t, path))
	if err != nil {
		t.Fatalf("decode %q: %v", path, err)
	}
	return cookies
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
	path := filepath.Join(t.TempDir(), "cookies.bin")
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
	c := cookieFileClient(t, filepath.Join(t.TempDir(), "does-not-exist.bin"))
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

func TestLoadCookiesEmptyFileIsAnEmptyState(t *testing.T) {
	path := cookieFileFixturePath(t)
	writeCookieFixture(t, path)
	c := cookieFileClient(t, path)
	if err := c.LoadCookies(); err != nil {
		t.Fatalf("LoadCookies: %v", err)
	}
	if n := len(c.store.persist); n != 0 {
		t.Errorf("persist size = %d, want 0", n)
	}
}

// TestLoadReportsACorruptFileAsAnError: a file that cannot be read is not the
// same thing as a session that was never stored, so it is reported rather than
// silently treated as "no cookies".
func TestLoadReportsACorruptFileAsAnError(t *testing.T) {
	cases := []struct {
		name    string
		data    []byte
		wantErr error
	}{
		{
			name:    "not a container at all",
			data:    []byte("# Netscape HTTP Cookie File\n.gog.com\tTRUE\t/\tFALSE\t0\tSID\tv\n"),
			wantErr: secretfile.ErrMagic,
		},
		{
			name:    "an empty file",
			data:    nil,
			wantErr: secretfile.ErrTruncated,
		},
		{
			// The framing is intact; the records inside it are not. The
			// failure must be the cookie layer's, not one of the five
			// framing errors.
			name:    "a payload that is not a record sequence",
			data:    encodeRawPayload([]byte{0x80}), // a flag byte with no defined bit
			wantErr: cookiefile.ErrMalformedPayload,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := cookieFileFixturePath(t)
			if err := os.WriteFile(path, c.data, 0o600); err != nil {
				t.Fatal(err)
			}
			client := cookieFileClient(t, path)
			err := client.LoadCookies()
			if !errors.Is(err, c.wantErr) {
				t.Errorf("LoadCookies = %v, want %v", err, c.wantErr)
			}
			if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("%q", path)) {
				t.Errorf("error %v must name the file it could not read", err)
			}
			if n := len(client.store.persist); n != 0 {
				t.Errorf("persist size = %d, want 0 after a failed load", n)
			}
		})
	}
}

// encodeRawPayload frames payload the way SaveCookies does, so a test can hand
// LoadCookies a container that is well-formed but holds anything it likes.
func encodeRawPayload(payload []byte) []byte {
	return secretfile.Encode(cookieFileMagic, cookieFileVersion, cookieFileObfuscation, payload)
}

func TestLoadReconstructsHostOnlyCookie(t *testing.T) {
	path := cookieFileFixturePath(t)
	writeCookieFixture(t, path, hostOnlyCookie("www.example.com", "/", "SID", "v1"))
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
	path := cookieFileFixturePath(t)
	writeCookieFixture(t, path, domainCookie("example.com", "/", "DOM", "v2"))
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

// TestLoadCompletesDefaultPath checks that a record without a path is completed
// with the same RFC default-path helper used when recording events.
func TestLoadCompletesDefaultPath(t *testing.T) {
	path := cookieFileFixturePath(t)
	writeCookieFixture(t, path, hostOnlyCookie("example.com", "", "SID", "v3"))
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
	path := cookieFileFixturePath(t)
	secure := hostOnlyCookie("example.com", "/", "SEC", "v4")
	secure.Secure = true
	writeCookieFixture(t, path, secure)
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

// TestHttpOnlySurvivesASaveAndLoad locks that the flag is stored and read back
// as itself: it is a field of the record, so nothing about it may depend on how
// a domain is written.
func TestHttpOnlySurvivesASaveAndLoad(t *testing.T) {
	path := cookieFileFixturePath(t)
	httpOnly := hostOnlyCookie("example.com", "/", "SID", "v5")
	httpOnly.HttpOnly = true
	writeCookieFixture(t, path, httpOnly)

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

	records := readCookieFile(t, path)
	if len(records) != 1 || !records[0].HttpOnly || !records[0].HostOnly {
		t.Errorf("stored records = %+v, want one httpOnly host-only record", records)
	}
	if records[0].Domain != "example.com" {
		t.Errorf("domain = %q, want it unchanged by the round trip", records[0].Domain)
	}
}

// TestLoadSessionCookieStaysSession locks that an expiry of 0 means "session
// cookie", which is persisted and reloaded as a session cookie, not as a
// cookie with an invented expiry.
func TestLoadSessionCookieStaysSession(t *testing.T) {
	path := cookieFileFixturePath(t)
	writeCookieFixture(t, path, hostOnlyCookie("example.com", "/", "SESS", "v6"))
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
	records := readCookieFile(t, path)
	if len(records) != 1 || !records[0].Expires.IsZero() {
		t.Errorf("session cookie not stored with expiry 0: %+v", records)
	}
}

// TestLoadFeedsEventsThroughRecord proves Load goes through the SetCookies
// event path rather than writing persist directly: an already-expired record is
// refused by record, exactly as a live expired event would be.
func TestLoadFeedsEventsThroughRecord(t *testing.T) {
	path := cookieFileFixturePath(t)
	expired := hostOnlyCookie("example.com", "/", "OLD", "v7")
	expired.Expires = time.Unix(1, 0) // 1970 -> past
	writeCookieFixture(t, path, expired)

	c := cookieFileClient(t, path)
	if err := c.LoadCookies(); err != nil {
		t.Fatalf("LoadCookies: %v", err)
	}
	if n := len(c.store.persist); n != 0 {
		t.Errorf("persist size = %d, want 0 (expired record must not enter persist)", n)
	}
}

func TestSaveDropsEntriesExpiredSinceRecording(t *testing.T) {
	path := cookieFileFixturePath(t)
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
	if records := readCookieFile(t, path); len(records) != 0 {
		t.Errorf("expired cookie was written: %+v", records)
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

	forward := save(filepath.Join(dir, "fwd.bin"), []int{0, 1, 2})
	reverse := save(filepath.Join(dir, "rev.bin"), []int{2, 1, 0})
	if !bytes.Equal(forward, reverse) {
		t.Errorf("insertion order changed the file:\nforward=%q\nreverse=%q", forward, reverse)
	}

	records := readCookieFile(t, filepath.Join(dir, "fwd.bin"))
	if len(records) != 3 ||
		records[0].Domain != "alpha.example" || records[1].Domain != "mid.example" || records[2].Domain != "zeta.example" {
		t.Errorf("records not sorted by domain: %+v", records)
	}
}

func TestSaveReplacesFileAtomicallyWith0600(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies.bin")
	if err := os.WriteFile(path, []byte("stale garbage without a container\n"), 0o644); err != nil {
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
	if !bytes.HasPrefix(data, []byte(cookieFileMagic)) {
		t.Errorf("file does not start with the container magic: %q", data)
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

// TestStoredCookieFileIsNotPlaintextAndStillSends is the round's central guard,
// in both halves at once: the file must not carry the session cookie in the
// clear, and a session restored from it must still send that cookie. The first
// half alone would be satisfied by a file nothing can read, so the request the
// restored jar makes is part of the same test.
func TestStoredCookieFileIsNotPlaintextAndStillSends(t *testing.T) {
	const (
		name  = "SIDSENTINEL"
		value = "COOKIE-VALUE-SENTINEL-4f7c"
	)
	path := filepath.Join(t.TempDir(), "cookies.bin")

	var mu sync.Mutex
	var sent [][]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/set" {
			http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/"})
			return
		}
		mu.Lock()
		sent = append(sent, cookiePairs(r.Cookies()))
		mu.Unlock()
	}))
	t.Cleanup(srv.Close)

	src := cookieFileClient(t, path)
	if _, err := src.Get(context.Background(), srv.URL+"/set"); err != nil {
		t.Fatalf("Get(/set): %v", err)
	}
	if _, err := src.SaveCookies(); err != nil {
		t.Fatalf("SaveCookies: %v", err)
	}

	data := mustRead(t, path)
	for _, needle := range []string{value, name} {
		if bytes.Contains(data, []byte(needle)) {
			t.Errorf("the stored file contains %q in the clear", needle)
		}
	}
	if !bytes.HasPrefix(data, []byte(cookieFileMagic)) {
		t.Errorf("stored file starts with %q, want the container magic", data[:min(len(data), len(cookieFileMagic))])
	}

	dst := cookieFileClient(t, path)
	if err := dst.LoadCookies(); err != nil {
		t.Fatalf("LoadCookies: %v", err)
	}
	if _, err := dst.Get(context.Background(), srv.URL+"/check"); err != nil {
		t.Fatalf("Get(/check): %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(sent) != 1 {
		t.Fatalf("the fixture served %d authenticated requests, want 1: the cookie was never sent", len(sent))
	}
	if want := []string{name + "=" + value}; !slices.Equal(sent[0], want) {
		t.Errorf("the request carried %v, want %v", sent[0], want)
	}
}

// TestRoundTripPreservesSendBehaviourAndBytes is the integration test: state
// saved by one client and loaded by a fresh one must send the same cookies, and
// re-saving must produce identical bytes.
func TestRoundTripPreservesSendBehaviourAndBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies.bin")

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
	path := filepath.Join(t.TempDir(), "cookies.bin")
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
	if records := readCookieFile(t, path); len(records) != 1 {
		t.Errorf("records = %+v, want one SID record", records)
	}
}
