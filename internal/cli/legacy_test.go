package cli

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/nekrozis/goggo/internal/core"
)

// legacyStoreName is the token file an earlier build wrote at the configuration
// root. Nothing reads it now, and this round exists to make that true rather
// than merely intended, so the name appears only in this file.
const legacyStoreName = "galaxy_tokens.json"

// TestALegacyTokenFileLeavesTheSessionLoggedOut locks the storage break at the
// level a user sees: with only an earlier build's token file on disk, opening a
// session finds no credential, so no request carries an Authorization header and
// no command reports success.
//
// Two details of the assertion are deliberate. It looks at the store that is
// read rather than at the cookie jar — a session with no credential is logged out
// whatever cookies exist, and keeping the two apart stops a later change to
// cookie storage from having to reinterpret this evidence. And the planted token
// is well-formed and unexpired, so a pass cannot come from "the token happened to
// be bad".
func TestALegacyTokenFileLeavesTheSessionLoggedOut(t *testing.T) {
	const sentinel = "LEGACY-SENTINEL-7d2b"

	isolateRoots(t)
	cfg, err := newConfig()
	if err != nil {
		t.Fatalf("newConfig: %v", err)
	}
	if err := os.MkdirAll(cfg.ConfigDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(cfg.ConfigDirectory, legacyStoreName)
	body := fmt.Sprintf(`{"access_token":%q,"refresh_token":%q,"expires_at":99999999999}`, sentinel, sentinel)
	if err := os.WriteFile(legacy, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var authHeaders []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		authHeaders = append(authHeaders, r.Header.Get("Authorization"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{}`)
	}))
	t.Cleanup(srv.Close)
	target, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	deps := core.Dependencies{HTTPTransport: &hostRedirectTransport{target: target}}

	code, out, errOut := runReference(t, deps, "list", "games")
	if code == 0 {
		t.Errorf("exit = 0, want a failure: the session must not come from the legacy file\n%s%s", out, errOut)
	}

	mu.Lock()
	defer mu.Unlock()
	for _, h := range authHeaders {
		if h != "" {
			t.Errorf("a request carried Authorization %q, want none: no credential was loaded", h)
		}
	}
	for name, s := range map[string]string{"stdout": out, "stderr": errOut} {
		if strings.Contains(s, sentinel) {
			t.Errorf("%s leaked the legacy sentinel:\n%s", name, s)
		}
	}
}

// TestTheLegacyFileSurvivesAnAuthCommand: the break is about what this build
// reads, not about what it deletes. A command that touches the session must
// leave the earlier build's file byte-identical, because removing another
// program's authentication state is not this command's job.
func TestTheLegacyFileSurvivesAnAuthCommand(t *testing.T) {
	isolateRoots(t)
	cfg, err := newConfig()
	if err != nil {
		t.Fatalf("newConfig: %v", err)
	}
	if err := os.MkdirAll(cfg.ConfigDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(cfg.ConfigDirectory, legacyStoreName)
	const body = `{"access_token":"legacy-access-token","refresh_token":"r"}`
	if err := os.WriteFile(legacy, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	// clear is the command that removes authentication state, so it is the one
	// that could plausibly overreach.
	if _, _, errOut := runReference(t, core.Dependencies{}, "auth", "clear"); strings.Contains(errOut, "Error:") {
		t.Fatalf("auth clear: %s", errOut)
	}

	after, err := os.ReadFile(legacy)
	if err != nil {
		t.Fatalf("the legacy file was removed: %v", err)
	}
	if string(after) != body {
		t.Errorf("legacy file = %q, want it byte-identical", after)
	}
}

// legacyCookieName is the cookie file an earlier build wrote: Netscape columns
// beside the file this build keeps. Like the token file above, it is not this
// build's to read or to delete.
const legacyCookieName = "cookies.txt"

// TestClearAuthRemovesTheCookieFileAndSparesTheLegacyOne is the cookie half of the
// same rule, at the level a user sees: auth clear removes the file this build
// owns — the cookie path the configuration names, whatever that is — and leaves
// the earlier build's file byte-identical.
func TestClearAuthRemovesTheCookieFileAndSparesTheLegacyOne(t *testing.T) {
	isolateRoots(t)
	cfg, err := newConfig()
	if err != nil {
		t.Fatalf("newConfig: %v", err)
	}
	if err := os.MkdirAll(cfg.ConfigDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	const body = "# Netscape HTTP Cookie File\n.gog.com\tTRUE\t/\tFALSE\t0\tSID\tlegacy\n"
	legacy := filepath.Join(cfg.ConfigDirectory, legacyCookieName)
	if err := os.WriteFile(legacy, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.Curl.CookiePath, []byte("session state"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, _, errOut := runReference(t, core.Dependencies{}, "auth", "clear"); strings.Contains(errOut, "Error:") {
		t.Fatalf("auth clear: %s", errOut)
	}

	if _, err := os.Stat(cfg.Curl.CookiePath); !os.IsNotExist(err) {
		t.Errorf("stat(%q) = %v, want the cookie file gone", cfg.Curl.CookiePath, err)
	}
	after, err := os.ReadFile(legacy)
	if err != nil {
		t.Fatalf("the legacy cookie file was removed: %v", err)
	}
	if string(after) != body {
		t.Errorf("legacy cookie file = %q, want it byte-identical", after)
	}
}
