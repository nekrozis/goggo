package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nekrozis/goggo/internal/auth"
	"github.com/nekrozis/goggo/internal/core"
)

// The credential seam (internal/auth) and the URL-redaction boundary
// (internal/httpx) are locked from the outside here, not by calling the
// redactor: one sentinel is planted in every place a run holds a credential —
// the token store's access and refresh token, the cookie file's session
// cookie, and the secret the login flow reads from its input — and the REAL
// dispatcher is then driven through the failure classes whose diagnostics are
// built from a request URL.
//
// Every case asserts both halves at once: that the run really failed (exit
// code, the expected text, and the requests the fixture really served), and
// that neither stream carried the sentinel. The expected text names the
// sanitised URL where the class reports one, so a diagnostic that was emptied
// instead of redacted fails too.
const sentinel = "SENTINEL-SECRET-8f3a1c"

// sentinelProductDoc is one product with one windows installer. Its file
// entry's downlink is the JSON document the run resolves twice: once while
// acquiring the file, once while downloading it.
const sentinelProductDoc = `{"id":555,"slug":"sentinel_game","title":"Sentinel Game",` +
	`"images":{"icon":"//images.gog.com/icon.png","logo":"//images.gog.com/logo.jpg"},` +
	`"downloads":{"installers":[{"name":"base.exe","version":"1.0","count":1,"total_size":10,` +
	`"files":[{"id":"base.exe","downlink":"https://api.gog.com/dl/base.exe","size":10}],` +
	`"os":"windows","language":"en"}],"bonus_content":[],"patches":[],"language_packs":[]}}`

// sentinelFixture answers the documents one guarded run reads: the session
// probe, the account listing the name lookup uses, the owned-games list, one
// product, its downlink document and the OAuth endpoints. Two answers belong
// to the case: the URL the downlink document names (the download that fails)
// and the token endpoint's status.
type sentinelFixture struct {
	*httptest.Server

	mu          sync.Mutex
	download    string
	tokenStatus int
	requests    []string
}

func newSentinelFixture(t *testing.T) *sentinelFixture {
	t.Helper()
	f := &sentinelFixture{tokenStatus: http.StatusOK}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.requests = append(f.requests, r.URL.Path)
		download, tokenStatus := f.download, f.tokenStatus
		f.mu.Unlock()

		switch r.URL.Path {
		case "/www/account":
			fmt.Fprint(w, "account")
		case "/www/user/data/games":
			fmt.Fprint(w, `{"owned":["555"]}`)
		case "/www/account/getFilteredProducts":
			fmt.Fprint(w, `{"page":1,"totalPages":1,"products":[{"id":"555","slug":"sentinel_game"}]}`)
		case "/products/555":
			fmt.Fprint(w, sentinelProductDoc)
		case "/dl/base.exe":
			fmt.Fprintf(w, `{"downlink":%q}`, download)
		case "/token":
			if tokenStatus != http.StatusOK {
				http.Error(w, "fixture failure", tokenStatus)
				return
			}
			fmt.Fprint(w, `{"access_token":"at","refresh_token":"rt","expires_in":3600,"user_id":"u1"}`)
		case "/auth":
			fmt.Fprint(w, `<html><body><form><input name="login[_token]" value="tok"></form></body></html>`)
		case "/callback":
			fmt.Fprint(w, "callback")
		default:
			// The download URL's own answer: the status failure the
			// token-carrying URL has to be reported through.
			http.Error(w, "fixture failure", http.StatusNotFound)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

// setDownload is the URL the downlink document names: the one the transfer
// fetches, and the one the failure is about.
func (f *sentinelFixture) setDownload(rawURL string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.download = rawURL
}

func (f *sentinelFixture) setTokenStatus(code int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tokenStatus = code
}

// hits counts the requests whose path is exactly want.
func (f *sentinelFixture) hits(want string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	var n int
	for _, path := range f.requests {
		if path == want {
			n++
		}
	}
	return n
}

// sentinelTransport is the run's network exit. goggo's hosts are answered by
// the local fixture — www and embed keep their host prefix, as
// hostRedirectTransport does — and the one request that must fail on the way
// out (a download aimed at a port nothing listens on) goes to the real
// transport. Any other host is refused here rather than sent to the network,
// so a run can never quietly reach one the guard does not expect.
//
// The clone is why the sentinel reaches the error path at all: only the clone
// is rewritten, so net/http builds its *url.Error from the ORIGINAL request
// URL, which is where a signed download URL carries its token.
type sentinelTransport struct{ target *url.URL }

func (t *sentinelTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	switch req.URL.Host {
	case "www.gog.com", "embed.gog.com", "api.gog.com", "content-system.gog.com",
		"cdn.gog.com", "auth.gog.com", "login.gog.com":
	default:
		if req.URL.Hostname() != "127.0.0.1" {
			return nil, fmt.Errorf("sentinel guard: unexpected host %q", req.URL.Host)
		}
		return http.DefaultTransport.RoundTrip(req)
	}
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

// sentinelDeps points every command at the fixture through that transport.
func sentinelDeps(t *testing.T, f *sentinelFixture) core.Dependencies {
	t.Helper()
	target, err := url.Parse(f.URL)
	if err != nil {
		t.Fatalf("parse fixture URL: %v", err)
	}
	return core.Dependencies{HTTPTransport: &sentinelTransport{target: target}}
}

// sentinelRoots isolates the config and cache roots and plants the sentinel in
// the two files the session reads: the token store (access and refresh token)
// and the cookie file (the session cookie). It returns the download root the
// download commands are pointed at, so a run never writes outside the
// temporary tree.
func sentinelRoots(t *testing.T, expiresAt int64) string {
	t.Helper()
	isolateRoots(t)
	cfg, err := newConfig()
	if err != nil {
		t.Fatalf("newConfig: %v", err)
	}
	if err := os.MkdirAll(cfg.ConfigDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	token := fmt.Sprintf(`{"access_token":%q,"refresh_token":%q,"expires_at":%d,"user_id":"u1"}`,
		sentinel, sentinel, expiresAt)
	// Seeded through the seam: the store's path and on-disk format are its own
	// business, and a guard that hard-coded either would not survive a change to
	// them — which is exactly the change this round makes.
	seed, err := auth.Open(auth.StorePath(cfg))
	if err != nil {
		t.Fatalf("auth.Open: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal([]byte(token), &fields); err != nil {
		t.Fatal(err)
	}
	seed.StoreLoginResponse(fields)
	if err := seed.Save(); err != nil {
		t.Fatalf("seed the session: %v", err)
	}
	// One session cookie, in the Netscape shape the cookie store reads back.
	cookies := "# Netscape HTTP Cookie File\n.gog.com\tTRUE\t/\tFALSE\t0\tSID\t" + sentinel + "\n"
	if err := os.WriteFile(cfg.Curl.CookiePath, []byte(cookies), 0o600); err != nil {
		t.Fatal(err)
	}
	return t.TempDir()
}

// sentinelExpiry is the token file's expires_at: a live token for the classes
// that need a session, and a passed one for the class that drives the refresh
// a run performs on an expired store.
func sentinelExpiry(expired bool) int64 {
	if expired {
		return time.Now().Add(-time.Hour).Unix()
	}
	return time.Now().Add(time.Hour).Unix()
}

// TestCredentialsNeverReachTheOutput drives the failure classes that report a
// request URL, one per class, with the sentinel in every credential the run
// holds.
func TestCredentialsNeverReachTheOutput(t *testing.T) {
	cases := []struct {
		name string
		args []string
		// download is the URL the downlink document names.
		download string
		// stdin is what the command reads: the callback URL of a browser
		// login, or the e-mail and password of a headless one.
		stdin string
		// tokenStatus is the token endpoint's answer.
		tokenStatus int
		expired     bool
		// wantErr is the text the failure must carry on stderr, wantOut the
		// text stdout must carry.
		wantErr  []string
		wantOut  []string
		wantHits []string
	}{
		{
			name:     "HTTP status error whose URL carries the token",
			args:     []string{"download", "sentinel_game"},
			download: "https://cdn.gog.com/games/sentinel/base.exe?access_token=" + sentinel,
			wantErr: []string{"Failed:", "HTTP 404",
				"https://cdn.gog.com/games/sentinel/base.exe"},
			wantHits: []string{"/dl/base.exe", "/games/sentinel/base.exe"},
		},
		{
			name:     "transport failure whose URL carries the token",
			args:     []string{"download", "file", "sentinel_game/base.exe"},
			download: "http://127.0.0.1:1/x?access_token=" + sentinel,
			wantErr:  []string{"Failed: sentinel_game/base.exe", "http://127.0.0.1:1/x"},
			wantHits: []string{"/dl/base.exe"},
		},
		{
			name: "unparsable URL carrying the token",
			args: []string{"download", "file", "sentinel_game/base.exe"},
			// The host is what url.Parse rejects; the game segment keeps the
			// derived destination a name the filesystem accepts, so the failure
			// stays the unparsable URL and not a file-creation error.
			download: "http://[::1/sentinel_game/base.exe?access_token=" + sentinel,
			wantErr:  []string{"Failed: sentinel_game/base.exe", "http://[::1/sentinel_game/base.exe?"},
			wantHits: []string{"/dl/base.exe"},
		},
		{
			name:        "OAuth exchange failure whose URL carries the code",
			args:        []string{"auth", "login", "--browser"},
			download:    "https://cdn.gog.com/games/sentinel/base.exe",
			stdin:       "https://auth.gog.com/callback?code=" + sentinel + "\n",
			tokenStatus: http.StatusInternalServerError,
			wantErr:     []string{"token exchange", "HTTP 500"},
			wantHits:    []string{"/auth", "/token"},
		},
		{
			// The refresh the session performs on an expired store: the request
			// carries client_secret and the sentinel refresh token, and the
			// session discards its failure, so the run fails for the missing
			// session instead. The case proves the credential-carrying request
			// was made (wantHits) and that it produced nothing on either
			// stream.
			name:        "OAuth refresh failure whose URL carries the refresh token",
			args:        []string{"download", "sentinel_game"},
			download:    "https://cdn.gog.com/games/sentinel/base.exe",
			tokenStatus: http.StatusInternalServerError,
			expired:     true,
			wantErr:     []string{"not logged in"},
			wantHits:    []string{"/token"},
		},
		{
			// The password the prompt would receive is planted on the input the
			// login reads. The console prompts only when its input is a real
			// terminal, so a test process takes the headless branch, whose
			// diagnostic names the two files it would have used — config-like
			// output that must carry no credential either.
			name:    "headless login reports the files it would use",
			args:    []string{"auth", "login"},
			stdin:   "user@example.com\n" + sentinel + "\n",
			wantErr: []string{"no credentials available in a non-interactive session"},
			wantOut: []string{"cookies.txt", "credentials.bin"},
		},
	}

	for _, c := range cases {
		for _, verbose := range []bool{false, true} {
			name, args := c.name, append([]string{}, c.args...)
			if verbose {
				name, args = name+" --verbose", append(args, "--verbose")
			}
			t.Run(name, func(t *testing.T) {
				root := sentinelRoots(t, sentinelExpiry(c.expired))
				f := newSentinelFixture(t)
				f.setDownload(c.download)
				f.setTokenStatus(c.tokenStatus)
				if isDownloadCommand(args) {
					args = append(args, "--directory", root)
				}

				code, stdout, stderr := runSentinel(t, sentinelDeps(t, f), c.stdin, args...)

				// The failure happened: a guard over a run that did nothing
				// would be worth nothing.
				if code == 0 {
					t.Fatalf("exit = 0, want a failed run:\nstdout: %s\nstderr: %s", stdout, stderr)
				}
				for _, want := range c.wantErr {
					if !strings.Contains(stderr, want) {
						t.Errorf("stderr does not carry %q:\n%s", want, stderr)
					}
				}
				for _, want := range c.wantOut {
					if !strings.Contains(stdout, want) {
						t.Errorf("stdout does not carry %q:\n%s", want, stdout)
					}
				}
				for _, path := range c.wantHits {
					if f.hits(path) == 0 {
						t.Errorf("the fixture never served %s: the failure did not come from the request it is about", path)
					}
				}
				assertNoLeak(t, sentinel, stdout, stderr)
			})
		}
	}
}

// isDownloadCommand reports whether the command line opens a transfer, which
// is what decides whether --directory applies.
func isDownloadCommand(args []string) bool {
	return len(args) > 0 && args[0] == "download"
}

// runSentinel drives one command line through the real dispatcher, with stdin
// as the command's input.
func runSentinel(t *testing.T, deps core.Dependencies, stdin string, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut strings.Builder
	code := runWithDeps(args, strings.NewReader(stdin), &out, &errOut, deps)
	return code, out.String(), errOut.String()
}

// assertNoLeak fails when either stream carries the sentinel. Both streams are
// checked: a diagnostic may land on stdout (the event stream) or on stderr
// (the error line), and the invariant covers both.
func assertNoLeak(t *testing.T, sentinel, stdout, stderr string) {
	t.Helper()
	for name, s := range map[string]string{"stdout": stdout, "stderr": stderr} {
		if strings.Contains(s, sentinel) {
			t.Errorf("%s leaked the sentinel:\n%s", name, s)
		}
	}
}
