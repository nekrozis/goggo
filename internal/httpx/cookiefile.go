package httpx

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nekrozis/goggo/internal/cookiefile"
)

// cookieFileMode mirrors the token-file writer in internal/auth: 0600 is Unix
// semantics; on Windows no claim of equivalent ACL behaviour is made.
const cookieFileMode = 0o600

var (
	// ErrCookieFileNotConfigured is returned by SaveCookies when no
	// CookieFile was configured: the caller explicitly asked to persist
	// cookies but gave no destination. LoadCookies treats the same
	// configuration as a no-op (persistence simply not enabled).
	ErrCookieFileNotConfigured = errors.New("httpx: cookie file not configured")

	// ErrCookieFileUnsupported is returned by LoadCookies and SaveCookies
	// when the caller supplied its own HTTPClient together with a
	// CookieFile. The caller's jar cannot be replaced by a cookieStore, so
	// cookie persistence is unavailable rather than silently ignored.
	ErrCookieFileUnsupported = errors.New("httpx: cookie file unsupported for a caller-provided HTTPClient")
)

// SaveCookiesResult reports the outcome of SaveCookies. It stays minimal:
// Written counts the rows actually encoded, excluding cookies the Netscape
// format cannot represent (a TAB/CR/LF in a column), which the codec skips.
type SaveCookiesResult struct {
	Written int
}

// cookieEntry is one persisted cookie: its canonical key plus the state to
// write. It is the stable form a snapshot hands to file I/O.
type cookieEntry struct {
	key   cookieKey
	state cookieState
}

// LoadCookies restores cookie state from the configured CookieFile.
//
// It is an initialisation-time API, not a merge or snapshot-replacement API:
// the decoded cookies are fed through the same SetCookies event path the
// runtime uses, so the jar and the persistence state are rebuilt identically
// and the file's rows are treated as "this cookie still exists" (deletion
// events never appear in a cookies.txt file). cookiejar.Jar has no public
// "remove all cookies" API, so an existing jar is never cleared — call this
// on a fresh Client.
//
// Error layers are kept distinct: an unconfigured CookieFile is a no-op; a
// caller-provided HTTPClient yields ErrCookieFileUnsupported; a missing file
// is an empty state (normal on first run); any other I/O failure is wrapped
// with the path. Malformed rows are skipped by the codec, never by this layer.
func (c *Client) LoadCookies() error {
	if c.cookieFile == "" {
		return nil
	}
	if c.store == nil {
		return ErrCookieFileUnsupported
	}
	data, err := os.ReadFile(c.cookieFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("httpx: load cookie file %q: %w", c.cookieFile, err)
	}
	for _, pc := range cookiefile.Parse(data) {
		u := reconstructURL(pc)
		c.store.SetCookies(u, []*http.Cookie{reconstructCookie(pc, u)})
	}
	return nil
}

// SaveCookies writes the persistence state to the configured CookieFile.
//
// The state comes from the cookieStore snapshot only — never from
// Jar.Cookies, which is a match-filtered view and would silently lose
// cookies. The mutex is held just long enough to copy the snapshot, so file
// I/O cannot block concurrent SetCookies events.
func (c *Client) SaveCookies() (SaveCookiesResult, error) {
	if c.cookieFile == "" {
		return SaveCookiesResult{}, ErrCookieFileNotConfigured
	}
	if c.store == nil {
		return SaveCookiesResult{}, ErrCookieFileUnsupported
	}
	rows := fileCookies(c.store.snapshot(), c.store.now())
	n, err := writeCookieFile(c.cookieFile, rows)
	if err != nil {
		return SaveCookiesResult{}, fmt.Errorf("httpx: save cookie file %q: %w", c.cookieFile, err)
	}
	return SaveCookiesResult{Written: n}, nil
}

// snapshot copies the persistence state into a stable slice. It holds s.mu
// only for the copy; the returned slice is independent of later events.
func (s *cookieStore) snapshot() []cookieEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]cookieEntry, 0, len(s.persist))
	for k, st := range s.persist {
		out = append(out, cookieEntry{key: k, state: st})
	}
	return out
}

// reconstructURL builds the URL a persisted cookie is replayed against. It is
// the minimal URL that lets the jar accept the cookie: host is the cookie's
// domain (a leading dot is only a file-format convention and is dropped here)
// and the scheme honours Secure so a secure cookie is not replayed over http.
// The path is the cookie's own path when it has one.
func reconstructURL(pc cookiefile.PersistentCookie) *url.URL {
	scheme := "http"
	if pc.Secure {
		scheme = "https"
	}
	u := &url.URL{Scheme: scheme, Host: strings.TrimPrefix(pc.Domain, ".")}
	if pc.Path != "" {
		u.Path = pc.Path
	}
	return u
}

// reconstructCookie turns a file row back into a Set-Cookie equivalent. The
// host-only / domain distinction is carried by Domain: an empty Domain means
// host-only to the jar, so the file's Domain must NOT be copied into it for
// host-only rows. A missing path is completed with the same RFC default-path
// helper used when recording events (no second implementation).
func reconstructCookie(pc cookiefile.PersistentCookie, u *url.URL) *http.Cookie {
	path := pc.Path
	if path == "" {
		path = defaultPath(u)
	}
	c := &http.Cookie{
		Name:     pc.Name,
		Value:    pc.Value,
		Path:     path,
		Secure:   pc.Secure,
		HttpOnly: pc.HttpOnly,
		Expires:  pc.Expires,
	}
	if !pc.HostOnly {
		c.Domain = u.Host
	}
	return c
}

// fileCookies converts a snapshot into file rows: deterministic order, the
// conventional leading dot on domain cookies, and entries that have expired
// since they were recorded are dropped. Session cookies (zero expiry) are
// KEPT and written as expiry 0 — "session" describes a cookie's lifetime, it
// is not a reason to omit the row.
func fileCookies(snap []cookieEntry, now time.Time) []cookiefile.PersistentCookie {
	out := make([]cookiefile.PersistentCookie, 0, len(snap))
	for _, e := range snap {
		if !e.state.expires.IsZero() && e.state.expires.Before(now) {
			continue
		}
		domain := e.key.domain
		if !e.key.hostOnly {
			domain = "." + domain
		}
		out = append(out, cookiefile.PersistentCookie{
			Expires:  e.state.expires,
			Domain:   domain,
			Path:     e.key.path,
			Name:     e.key.name,
			Value:    e.state.value,
			Secure:   e.state.secure,
			HttpOnly: e.state.httpOnly,
			HostOnly: e.key.hostOnly,
		})
	}
	// Keys are unique (domain, path, name, hostOnly), so this is a total
	// order: equal content always produces byte-identical output.
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Domain != b.Domain {
			return a.Domain < b.Domain
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		// Domain cookies (HostOnly=false) precede host-only ones; the
		// four-tuple is unique, so the order is total.
		return !a.HostOnly && b.HostOnly
	})
	return out
}

// writeCookieFile writes rows to path atomically: a temp file in the same
// directory (created 0600 before any content is written) is written, synced,
// closed and renamed over path. The same-directory temp keeps the rename on
// one filesystem. As with the token-file writer, this prevents a truncated
// file on the normal path but claims no cross-platform crash durability; on
// Windows the replace semantics of os.Rename apply as-is.
func writeCookieFile(path string, cookies []cookiefile.PersistentCookie) (int, error) {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".goggo-cookies-*")
	if err != nil {
		return 0, fmt.Errorf("create temp cookie file in %q: %w", dir, err)
	}
	if err := tmp.Chmod(cookieFileMode); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return 0, fmt.Errorf("chmod temp cookie file: %w", err)
	}
	n, err := cookiefile.Write(tmp, cookies)
	if err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return 0, fmt.Errorf("encode cookies: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return 0, fmt.Errorf("sync temp cookie file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return 0, fmt.Errorf("close temp cookie file: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return 0, fmt.Errorf("replace cookie file: %w", err)
	}
	return n, nil
}
