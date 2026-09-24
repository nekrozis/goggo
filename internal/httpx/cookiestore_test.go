package httpx

import (
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"sync"
	"testing"
	"time"
)

// fixedNow is a deterministic clock for persistence expiry tests.
var fixedNow = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

func mustParse(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", raw, err)
	}
	return u
}

func newTestStore() *cookieStore {
	s := newCookieStore()
	s.now = func() time.Time { return fixedNow }
	return s
}

func hostCookie(name, value string) *http.Cookie {
	return &http.Cookie{Name: name, Value: value}
}

func equalCookies(a, b []*http.Cookie) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name || a[i].Value != b[i].Value ||
			a[i].Domain != b[i].Domain || a[i].Path != b[i].Path {
			return false
		}
	}
	return true
}

func TestSetCookiesPersistsNewCookie(t *testing.T) {
	s := newTestStore()
	u := mustParse(t, "https://gog.com:443/account/settings")
	s.SetCookies(u, []*http.Cookie{hostCookie("SID", "abc")})

	key := cookieKey{domain: "gog.com", path: "/account", name: "SID", hostOnly: true} // port stripped, default path
	st, ok := s.persist[key]
	if !ok {
		t.Fatalf("persist missing key %+v; have %v", key, s.persist)
	}
	if st.value != "abc" || st.expires != (time.Time{}) || st.secure || st.httpOnly {
		t.Errorf("state = %+v", st)
	}
}

// TestKeyCanonicalisation locks host-only domain derivation: u.Hostname
// (never u.Host, so ports are stripped and IPv6 brackets removed) and the
// domain-cookie form (lowercase, leading dot removed).
func TestKeyCanonicalisation(t *testing.T) {
	s := newTestStore()
	u := mustParse(t, "https://Example.COM:443/")
	s.SetCookies(u, []*http.Cookie{hostCookie("a", "1")})
	if _, ok := s.persist[cookieKey{domain: "example.com", path: "/", name: "a", hostOnly: true}]; !ok {
		t.Errorf("host-only key not canonicalised to example.com")
	}

	ipv6 := mustParse(t, "https://[::1]:8443/x")
	s.SetCookies(ipv6, []*http.Cookie{hostCookie("b", "2")})
	if _, ok := s.persist[cookieKey{domain: "::1", path: "/", name: "b", hostOnly: true}]; !ok {
		t.Errorf("IPv6 host-only key must be ::1 (no brackets)")
	}

	dom := mustParse(t, "https://sub.gog.com/")
	s.SetCookies(dom, []*http.Cookie{{Name: "c", Value: "3", Domain: ".GOG.com"}})
	if _, ok := s.persist[cookieKey{domain: "gog.com", path: "/", name: "c"}]; !ok {
		t.Errorf("domain cookie key must be lowercase without leading dot")
	}
}

func TestDefaultPathTable(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{"https://gog.com/a/b/c", "/a/b"},
		{"https://gog.com/a", "/"},
		{"https://gog.com/", "/"},
		{"https://gog.com/a/", "/a"},
		{"https://gog.com", "/"},
	}
	for _, c := range cases {
		u := mustParse(t, c.raw)
		if got := defaultPath(u); got != c.want {
			t.Errorf("defaultPath(%q) = %q, want %q", c.raw, got, c.want)
		}
	}
}

func TestOverwriteSameKeyUpdatesValue(t *testing.T) {
	s := newTestStore()
	u := mustParse(t, "https://gog.com/")
	s.SetCookies(u, []*http.Cookie{hostCookie("SID", "old")})
	s.SetCookies(u, []*http.Cookie{hostCookie("SID", "new")})

	if got := s.persist[cookieKey{domain: "gog.com", path: "/", name: "SID", hostOnly: true}].value; got != "new" {
		t.Errorf("persist value = %q, want new (single entry)", got)
	}
	if n := len(s.persist); n != 1 {
		t.Errorf("persist size = %d, want 1", n)
	}
	// jar advanced in lock-step with persist.
	if cs := s.jar.Cookies(u); len(cs) != 1 || cs[0].Value != "new" {
		t.Errorf("jar = %+v, want single new", cs)
	}
}

func TestDeleteRemovesOnlyExactKey(t *testing.T) {
	s := newTestStore()
	u := mustParse(t, "https://gog.com/")
	s.SetCookies(u, []*http.Cookie{
		{Name: "SID", Value: "v", Path: "/foo"},
		{Name: "SID", Value: "w", Path: "/bar"},
	})
	// Delete /foo only.
	del := &http.Cookie{Name: "SID", Value: "", MaxAge: -1, Path: "/foo"}
	s.SetCookies(u, []*http.Cookie{del})

	if _, ok := s.persist[cookieKey{domain: "gog.com", path: "/foo", name: "SID", hostOnly: true}]; ok {
		t.Error("/foo cookie still persisted after its deletion event")
	}
	if _, ok := s.persist[cookieKey{domain: "gog.com", path: "/bar", name: "SID", hostOnly: true}]; !ok {
		t.Error("/bar cookie must survive a /foo deletion")
	}
}

func TestEmptyValueIsNotDeletion(t *testing.T) {
	s := newTestStore()
	u := mustParse(t, "https://gog.com/")
	s.SetCookies(u, []*http.Cookie{{Name: "SID", Value: ""}})
	key := cookieKey{domain: "gog.com", path: "/", name: "SID", hostOnly: true}
	if _, ok := s.persist[key]; !ok {
		t.Error("empty-value cookie must be upserted, not deleted")
	}
}

func TestMaxAgePositiveExpiry(t *testing.T) {
	s := newTestStore()
	u := mustParse(t, "https://gog.com/")
	s.SetCookies(u, []*http.Cookie{{Name: "a", Value: "1", MaxAge: 60}})
	key := cookieKey{domain: "gog.com", path: "/", name: "a", hostOnly: true}
	if got := s.persist[key].expires; !got.Equal(fixedNow.Add(60 * time.Second)) {
		t.Errorf("expires = %v, want now+60s (%v)", got, fixedNow.Add(60*time.Second))
	}
}

func TestExpiredCookieNotPersisted(t *testing.T) {
	s := newTestStore()
	u := mustParse(t, "https://gog.com/")
	past := fixedNow.Add(-time.Hour)
	s.SetCookies(u, []*http.Cookie{{Name: "a", Value: "1", Expires: past}})
	if len(s.persist) != 0 {
		t.Errorf("expired cookie was persisted: %v", s.persist)
	}
}

func TestMaxAgeWinsOverPastExpires(t *testing.T) {
	s := newTestStore()
	u := mustParse(t, "https://gog.com/")
	past := fixedNow.Add(-time.Hour)
	s.SetCookies(u, []*http.Cookie{{Name: "a", Value: "1", Expires: past, MaxAge: 60}})
	key := cookieKey{domain: "gog.com", path: "/", name: "a", hostOnly: true}
	if _, ok := s.persist[key]; !ok {
		t.Fatal("positive MaxAge must win over a past Expires (RFC 6265)")
	}
	if got := s.persist[key].expires; !got.Equal(fixedNow.Add(60 * time.Second)) {
		t.Errorf("expires = %v, want now+60s", got)
	}
}

func TestSecureAndHttpOnlyPreserved(t *testing.T) {
	s := newTestStore()
	u := mustParse(t, "https://gog.com/")
	s.SetCookies(u, []*http.Cookie{{Name: "a", Value: "1", Secure: true, HttpOnly: true}})
	st := s.persist[cookieKey{domain: "gog.com", path: "/", name: "a", hostOnly: true}]
	if !st.secure || !st.httpOnly {
		t.Errorf("state = %+v, want secure+httpOnly", st)
	}
}

// TestCookiesDelegatesToBareJar locks that request behaviour is unchanged:
// wrapper output equals a bare stdlib jar for the same inputs. This proves
// the wrapper adds no matching logic; it does NOT prove persist == jar.
func TestCookiesDelegatesToBareJar(t *testing.T) {
	bare, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	s := newTestStore()

	events := []struct {
		raw string
		cs  []*http.Cookie
	}{
		{"https://gog.com/", []*http.Cookie{hostCookie("SID", "abc")}},
		{"https://sub.gog.com/x", []*http.Cookie{{Name: "dom", Value: "1", Domain: ".gog.com", Path: "/"}}},
		{"https://gog.com/secure", []*http.Cookie{{Name: "s", Value: "2", Secure: true, Path: "/"}}},
	}
	probes := []string{
		"https://gog.com/",
		"https://gog.com/secure",
		"https://sub.gog.com/x",
		"https://other.example/",
	}

	for _, e := range events {
		u := mustParse(t, e.raw)
		s.SetCookies(u, e.cs)
		bare.SetCookies(u, e.cs)
	}
	for _, p := range probes {
		pu := mustParse(t, p)
		got, want := s.Cookies(pu), bare.Cookies(pu)
		if !equalCookies(got, want) {
			t.Errorf("Cookies(%q) diverged from bare jar:\nwrapper=%+v\nbare   =%+v",
				p, s.Cookies(pu), bare.Cookies(pu))
		}
	}
}

// TestSetCookiesEventOrderNoRegression locks the v4 ordering guarantee with a
// deterministic serial model: after Set(A) then Set(B) on the same key, both
// jar and persist hold B — persist never regresses to the earlier event.
func TestSetCookiesEventOrderNoRegression(t *testing.T) {
	u := mustParse(t, "https://gog.com/")
	for _, seq := range [][]string{{"A", "B"}, {"B", "A"}} {
		s := newTestStore()
		for _, v := range seq {
			s.SetCookies(u, []*http.Cookie{hostCookie("SID", v)})
		}
		key := cookieKey{domain: "gog.com", path: "/", name: "SID", hostOnly: true}
		// The invariant under test: the last complete event is visible in
		// BOTH jar and persist, whichever writer went last.
		last := seq[len(seq)-1]
		if got := s.persist[key].value; got != last {
			t.Errorf("persist = %q, want %q (no regression to earlier event)", got, last)
		}
		if cs := s.jar.Cookies(u); len(cs) != 1 || cs[0].Value != last {
			t.Errorf("jar = %+v, want %q", cs, last)
		}
	}
}

// TestConcurrentSetCookiesKeepsJarAndPersistAligned runs two writers on the
// same key under real concurrency and asserts the ending persist value equals
// the jar's ending value — i.e. no interleaving left persist behind at an
// earlier event. The mutex serialises each whole SetCookies, so jar and
// persist always advance together.
func TestConcurrentSetCookiesKeepsJarAndPersistAligned(t *testing.T) {
	s := newTestStore()
	u := mustParse(t, "https://gog.com/")
	key := cookieKey{domain: "gog.com", path: "/", name: "SID", hostOnly: true}

	const writers = 4
	const perWriter = 100
	var wg sync.WaitGroup
	for w := range writers {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := range perWriter {
				s.SetCookies(u, []*http.Cookie{hostCookie("SID", fmt.Sprintf("w%d-%d", w, i))})
			}
		}(w)
	}
	wg.Wait()

	persistVal := s.persist[key].value
	var jarVal string
	if cs := s.jar.Cookies(u); len(cs) == 1 {
		jarVal = cs[0].Value
	}
	if persistVal == "" || persistVal != jarVal {
		t.Errorf("persist %q diverged from jar %q after concurrent SetCookies", persistVal, jarVal)
	}
}
