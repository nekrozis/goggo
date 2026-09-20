package cli

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/nekrozis/goggo/internal/auth"
	"github.com/nekrozis/goggo/internal/core"
)

// This file is the dispatch coverage guard. `auth login` completed its login,
// stored the credentials and then reported "auth login has no handler" — the
// post-session switch had no case for it and every command id that reaches the
// switch had to be listed there by hand.
//
// The guard is behavioural, not a hand-kept list: it drives EVERY leaf of the
// command tree through the real dispatcher against a local server and fails if
// any of them ends in the no-handler default. A new command therefore cannot
// ship without a handler — the omission fails here instead of in the user's
// terminal.

// dispatchFixture answers the requests a session needs: the login endpoints,
// the account probe, the token exchange. Anything else is answered with a
// benign empty object, so a command that needs more fails with its OWN error —
// which is all this test needs, because the assertion is about the dispatcher,
// not about what each command does.
type dispatchFixture struct {
	*httptest.Server

	mu    sync.Mutex
	paths []string
}

func newDispatchFixture(t *testing.T) *dispatchFixture {
	t.Helper()
	f := &dispatchFixture{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.paths = append(f.paths, r.URL.Path)
		f.mu.Unlock()
		switch r.URL.Path {
		case "/www/account":
			fmt.Fprint(w, "account")
		case "/token":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"access_token":"at","refresh_token":"rt","expires_in":3600,"user_id":"u1"}`)
		case "/auth":
			fmt.Fprint(w, `<html><body><form><input name="login[_token]" value="tok"></form></body></html>`)
		case "/login_check":
			http.Redirect(w, r, "/callback?code=plain-code", http.StatusFound)
		case "/callback":
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "jar-cookie", Path: "/"})
			fmt.Fprint(w, "callback")
		default:
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{}`)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

// hostRedirectTransport points goggo's hosts at the fixture, keeping the www
// and embed probes apart exactly as production does (the core seam's contract).
type hostRedirectTransport struct{ target *url.URL }

func (t *hostRedirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	u := *req.URL
	u.Scheme, u.Host = t.target.Scheme, t.target.Host
	switch req.URL.Host {
	case "www.gog.com", "embed.gog.com":
		u.Path = "/" + strings.SplitN(req.URL.Host, ".", 2)[0] + req.URL.Path
	}
	clone.URL = &u
	return http.DefaultTransport.RoundTrip(clone)
}

// isolateRoots points the XDG/stdlib roots at a temporary directory, so a test
// that reaches the session never touches the developer's real configuration.
func isolateRoots(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("AppData", dir)         // Windows: os.UserConfigDir
	t.Setenv("XDG_CONFIG_HOME", dir) // Unix
	t.Setenv("XDG_CACHE_HOME", dir)  // Unix
	t.Setenv("HOME", dir)            // Unix fallback
	t.Setenv("LOCALAPPDATA", dir)    // Windows cache
}

// leafInvocations builds one runnable command line per leaf of the tree: the
// verb path plus whatever its arity needs. auth login is driven through
// --browser, whose flow reads the callback URL from stdin.
func leafInvocations(t *testing.T) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	var walk func(path []string, nodes []commandNode)
	walk = func(path []string, nodes []commandNode) {
		for _, n := range nodes {
			here := append(append([]string{}, path...), n.name)
			if len(n.children) != 0 && n.id == cmdNone {
				walk(here, n.children)
				continue
			}
			args := append([]string{}, here...)
			if _, count := commandArity(n.id); count != 0 {
				switch count {
				case 1, -1:
					args = append(args, "some_game")
				case -2:
					// zero-or-more: an empty selection is legal
				}
			}
			if n.id == cmdAuthLogin {
				args = append(args, "--browser")
			}
			out[strings.Join(here, " ")] = args
			if len(n.children) != 0 {
				// A node that is both leaf and namespace ("download").
				walk(here, n.children)
			}
		}
	}
	walk(nil, commandTree)
	return out
}

// TestEveryCommandReachesAHandler is the regression guard for the class of bug
// that made a successful `auth login` report "auth login has no handler".
func TestEveryCommandReachesAHandler(t *testing.T) {
	isolateRoots(t)
	f := newDispatchFixture(t)
	target, err := url.Parse(f.URL)
	if err != nil {
		t.Fatalf("parse fixture URL: %v", err)
	}
	deps := core.Dependencies{HTTPTransport: &hostRedirectTransport{target: target}}

	invs := leafInvocations(t)
	if len(invs) < len(commandTree) {
		t.Fatalf("only %d invocations for %d top-level nodes", len(invs), len(commandTree))
	}
	for name, args := range invs {
		t.Run(name, func(t *testing.T) {
			var out, errOut strings.Builder
			// The browser login reads the callback URL from stdin.
			code := runWithDeps(args, strings.NewReader("https://auth.gog.com/callback?code=pasted\n"), &out, &errOut, deps)
			if strings.Contains(errOut.String(), "has no handler") {
				t.Fatalf("goggo %s reached the no-handler default (exit %d):\n%s",
					strings.Join(args, " "), code, errOut.String())
			}
			if strings.Contains(errOut.String(), "unknown command") {
				t.Fatalf("goggo %s is not in the tree: %s", strings.Join(args, " "), errOut.String())
			}
			// The handler's own error is fine — the fixture answers only what a
			// session needs — but the exit code still has to come from the
			// documented set (run.go is its single authority). A panic or a
			// stray code is a dispatch bug, not a command's answer.
			switch code {
			case 0, 1, 2, 130:
			default:
				t.Fatalf("goggo %s exited %d, want one of 0/1/2/130:\n%s",
					strings.Join(args, " "), code, errOut.String())
			}
		})
	}
}

// TestAuthLoginSucceedsAndExitsZero locks the specific contract the gap broke:
// a login that completes reports success. The fixture makes the browser flow
// finish, so the command must exit 0 — not "has no handler" (exit 1) over a
// session it just stored.
func TestAuthLoginSucceedsAndExitsZero(t *testing.T) {
	isolateRoots(t)
	f := newDispatchFixture(t)
	target, err := url.Parse(f.URL)
	if err != nil {
		t.Fatalf("parse fixture URL: %v", err)
	}
	deps := core.Dependencies{HTTPTransport: &hostRedirectTransport{target: target}}

	var out, errOut strings.Builder
	code := runWithDeps([]string{"auth", "login", "--browser"},
		strings.NewReader("https://auth.gog.com/callback?code=pasted\n"), &out, &errOut, deps)
	if code != 0 {
		t.Fatalf("auth login exit = %d, want 0 (stderr: %s)", code, errOut.String())
	}
	if strings.Contains(errOut.String(), "has no handler") {
		t.Fatalf("auth login reported the no-handler default: %s", errOut.String())
	}
	// The login really happened: the credential file the production writer stores is
	// on disk under the isolated root.
	cfg, err := newConfig()
	if err != nil {
		t.Fatalf("newConfig: %v", err)
	}
	if _, err := os.Stat(auth.StorePath(cfg)); err != nil {
		t.Errorf("credential file %q was not written: %v", auth.StorePath(cfg), err)
	}
	if _, err := os.Stat(cfg.Curl.CookiePath); err != nil {
		t.Errorf("cookie file %q was not written: %v", cfg.Curl.CookiePath, err)
	}
}
