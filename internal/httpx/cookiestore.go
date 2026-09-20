package httpx

import (
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"time"
)

// cookieStore is the http.CookieJar handed to http.Client when cookie-file
// persistence is configured.
//
// s.jar is the single authority for cookie matching and sending: Cookies(u)
// delegates to it and never reads the persistence state. SetCookies additionally
// records each cookie EVENT into s.persist so the state can be reconstructed
// across processes (LoadCookies/SaveCookies). persist is NOT a copy of the jar's
// accepted state — cookies the jar rejects may still be recorded, and on reload
// the jar filters them again.
//
// Lock order: mu serialises the whole SetCookies event so jar and persist
// advance together and persist never regresses. Cookies takes the jar lock only
// and Save takes mu only, so there is no jar→mu path and no inversion.
type cookieStore struct {
	jar *cookiejar.Jar
	mu  sync.Mutex
	// persist keys are produced by keyFor only, so record/delete/load never
	// re-implement canonicalisation.
	persist map[cookieKey]cookieState
	// now is injectable per instance for deterministic tests (no global).
	now func() time.Time
}

// cookieKey identifies one persisted cookie. All canonicalisation lives in
// keyFor: domain is lowercase with any leading dot removed (host-only cookies
// use the request hostname), path carries the RFC default when the Set-Cookie
// had none, name is exact.
type cookieKey struct {
	domain   string
	path     string
	name     string
	hostOnly bool
}

// cookieState is the persistable subset of a cookie event. expires is the
// value to store (zero = session cookie).
type cookieState struct {
	expires  time.Time
	value    string
	secure   bool
	httpOnly bool
}

// newCookieStore builds a cookieStore backed by a fresh standard jar.
func newCookieStore() *cookieStore {
	jar, err := cookiejar.New(nil)
	if err != nil {
		// cookiejar.New(nil) never fails; keep the error path explicit.
		panic(err)
	}
	return &cookieStore{
		jar:     jar,
		persist: make(map[cookieKey]cookieState),
		now:     time.Now,
	}
}

// Cookies returns the cookies the jar would send to u. It never consults
// persist and never takes s.mu.
func (s *cookieStore) Cookies(u *url.URL) []*http.Cookie {
	return s.jar.Cookies(u)
}

// SetCookies feeds u/cs to the authoritative jar and records the persistence
// events. The whole sequence runs under s.mu so concurrent callers cannot
// interleave jar and persist updates. A single now is
// used for the whole batch so one event has no intra-batch time drift.
func (s *cookieStore) SetCookies(u *url.URL, cookies []*http.Cookie) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	s.jar.SetCookies(u, cookies)
	for _, c := range cookies {
		s.record(now, u, c)
	}
}

// record applies one cookie event to persist. It runs with s.mu held.
func (s *cookieStore) record(now time.Time, u *url.URL, c *http.Cookie) {
	if c == nil {
		return
	}
	key := keyFor(u, c)

	if c.MaxAge < 0 {
		// Deletion event: remove exactly the key this event denotes. It never
		// scans for same-named cookies under other paths/domains.
		delete(s.persist, key)
		return
	}

	// An already-expired cookie (no positive MaxAge) is not persisted:
	// reconstruction does not need it. This is a persistence decision, NOT a
	// claim that the jar deleted it internally.
	if c.MaxAge == 0 && !c.Expires.IsZero() && c.Expires.Before(now) {
		return
	}

	s.persist[key] = cookieState{
		expires:  persistExpiry(c, now),
		value:    c.Value,
		secure:   c.Secure,
		httpOnly: c.HttpOnly,
	}
}

// keyFor canonicalises a cookie event into its persistence key. hostOnly is
// derived from the absence of an explicit Domain attribute; the key domain is
// the request hostname (lowercased, port stripped) for host-only cookies and
// the canonicalised Domain otherwise. Leading dots are never kept in the key.
func keyFor(u *url.URL, c *http.Cookie) cookieKey {
	hostOnly := c.Domain == ""
	domain := ""
	if hostOnly {
		domain = strings.ToLower(u.Hostname())
	} else {
		domain = strings.ToLower(strings.TrimPrefix(c.Domain, "."))
	}
	path := c.Path
	if path == "" {
		path = defaultPath(u)
	}
	return cookieKey{domain: domain, path: path, name: c.Name, hostOnly: hostOnly}
}

// persistExpiry decides the expiry to persist: a positive MaxAge wins over
// Expires (RFC 6265 semantics); otherwise an explicit Expires is kept; a
// cookie with neither is a session cookie (zero time). now is injected so the
// whole SetCookies batch shares one clock.
func persistExpiry(c *http.Cookie, now time.Time) time.Time {
	if c.MaxAge > 0 {
		return now.Add(time.Duration(c.MaxAge) * time.Second)
	}
	if !c.Expires.IsZero() {
		return c.Expires
	}
	return time.Time{}
}

// defaultPath implements RFC 6265 section 5.1.4: the default path of a cookie
// whose Set-Cookie carried no Path attribute. The input is u.EscapedPath, the
// raw (undecoded) URI path, matching the request-target semantics the RFC's
// uri-path refers to.
//
// This is attribute completion for persistence, NOT request matching — the jar
// applies its own path matching when sending cookies.
func defaultPath(u *url.URL) string {
	p := u.EscapedPath()
	if p == "" || p[0] != '/' {
		return "/"
	}
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		p = p[:i]
	}
	if p == "" {
		return "/"
	}
	return p
}
