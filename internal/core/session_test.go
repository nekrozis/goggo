package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nekrozis/goggo/internal/auth"
	"github.com/nekrozis/goggo/internal/config"
	"github.com/nekrozis/goggo/internal/galaxy"
	"github.com/nekrozis/goggo/internal/httpx"
	"github.com/nekrozis/goggo/internal/webapi"
)

// fakeConsole is the front end a test drives: the streams it writes through, the
// answers it hands back and a record of what was asked for. It replaces the
// terminal, so the orchestration can be tested without one; the wording of the
// prompts themselves belongs to the CLI console and is tested there.
type fakeConsole struct {
	out    bytes.Buffer
	errOut bytes.Buffer

	answers     []string
	prompted    []string
	interactive bool

	selection    []string
	selected     int
	selectionErr error

	challenges []*webapi.LoginChallenge
}

func newFakeConsole(answers ...string) *fakeConsole {
	return &fakeConsole{answers: answers, interactive: true}
}

func (f *fakeConsole) Out() io.Writer    { return &f.out }
func (f *fakeConsole) ErrOut() io.Writer { return &f.errOut }
func (f *fakeConsole) IsTerminal() bool  { return f.interactive }

// next returns the next canned answer, so a test states exactly how many
// prompts it expects the flow to reach.
func (f *fakeConsole) next() (string, error) {
	if len(f.answers) == 0 {
		return "", errors.New("test console ran out of answers")
	}
	answer := f.answers[0]
	f.answers = f.answers[1:]
	return answer, nil
}

func (f *fakeConsole) PromptEmail() (string, error) {
	f.prompted = append(f.prompted, "email")
	return f.next()
}

func (f *fakeConsole) PromptPassword() (string, error) {
	f.prompted = append(f.prompted, "password")
	return f.next()
}

func (f *fakeConsole) ResolveChallenge(ctx context.Context, web *webapi.Client, ch *webapi.LoginChallenge) error {
	f.challenges = append(f.challenges, ch)
	answer, err := f.next()
	if err != nil {
		return err
	}
	return web.ContinueLogin(ctx, ch, answer)
}

func (f *fakeConsole) SelectProduct(items []string) (int, error) {
	f.selection = items
	return f.selected, f.selectionErr
}

// gogHostTransport maps the production GOG hosts onto a test server so a flow
// can be driven offline. Only the transport is doubled: the packages build the
// production URLs themselves, and what is under test is what the orchestration
// does with the answers.
type gogHostTransport struct{ target *url.URL }

func (t *gogHostTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	u := *req.URL
	u.Scheme, u.Host = t.target.Scheme, t.target.Host
	// Keep the www and embed account probes apart, as the production hosts are.
	switch req.URL.Host {
	case "www.gog.com", "embed.gog.com":
		u.Path = "/" + strings.SplitN(req.URL.Host, ".", 2)[0] + req.URL.Path
	}
	clone.URL = &u
	return http.DefaultTransport.RoundTrip(clone)
}

// loginTestServer models the interactive login outcomes: mode "two-step" and
// "totp" answer the form login with the matching one-time-code challenge,
// anything else completes the form login and leaves only the browser callback.
func loginTestServer(t *testing.T, mode string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/auth":
			fmt.Fprint(w, `<html><body><form><input name="login[_token]" value="tok"></form></body></html>`)
		case r.URL.Path == "/login_check":
			switch mode {
			case "two-step":
				http.Redirect(w, r, "/login/two_step", http.StatusFound)
			case "totp":
				http.Redirect(w, r, "/login/two_factor/totp", http.StatusFound)
			default:
				http.Redirect(w, r, "/callback?code=plain-code", http.StatusFound)
			}
		case r.URL.Path == "/login/two_step" && r.Method == http.MethodGet:
			fmt.Fprint(w, `<html><body><form><input name="second_step_authentication[_token]" value="csrf"></form></body></html>`)
		case r.URL.Path == "/login/two_step":
			http.Redirect(w, r, "/callback?code=two-step-code", http.StatusFound)
		case r.URL.Path == "/login/two_factor/totp" && r.Method == http.MethodGet:
			fmt.Fprint(w, `<html><body><form><input name="two_factor_totp_authentication[_token]" value="csrf"></form></body></html>`)
		case r.URL.Path == "/login/two_factor/totp":
			http.Redirect(w, r, "/callback?code=totp-code", http.StatusFound)
		case r.URL.Path == "/callback":
			fmt.Fprint(w, "callback")
		case r.URL.Path == "/token":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"access_token":"at-1","refresh_token":"rt-1","expires_in":3600,"user_id":"u1"}`)
		case r.URL.Path == "/www/account":
			fmt.Fprint(w, "account")
		default:
			http.NotFound(w, r)
		}
	}))
}

// newOfflineDownloader builds a Downloader whose transport is the rewriting
// double. The state is assembled here rather than through Open because Open
// builds its transport from the configuration; see the coverage note on
// TestInitRefreshesAndSavesExpiredToken.
func newOfflineDownloader(t *testing.T, srv *httptest.Server, cfg config.Config, ui Console) *Downloader {
	t.Helper()
	return newOfflineDownloaderWith(t, srv, cfg, ui, Dependencies{})
}

// newOfflineDownloaderWith is newOfflineDownloader with the injection seam the
// front end uses: the progress registry travels the same route, from
// Dependencies into the downloader and on into the transfer run. A test that
// hands in its own network exit gets it in place of the fixture's host rewrite,
// which is the same seam the front end has.
func newOfflineDownloaderWith(t *testing.T, srv *httptest.Server, cfg config.Config, ui Console, deps Dependencies) *Downloader {
	t.Helper()
	rt := deps.HTTPTransport
	if rt == nil {
		target, err := url.Parse(srv.URL)
		if err != nil {
			t.Fatalf("parse server URL: %v", err)
		}
		rt = &gogHostTransport{target: target}
	}
	hx, err := httpx.New(httpx.Config{HTTPClient: &http.Client{Transport: rt}})
	if err != nil {
		t.Fatalf("httpx.New: %v", err)
	}
	// The store is opened at the production location, so a run that refreshes or
	// logs in persists exactly where a real one does.
	store, err := auth.Open(auth.StorePath(cfg))
	if err != nil {
		t.Fatalf("auth.Open: %v", err)
	}
	web, err := webapi.New(hx, store)
	if err != nil {
		t.Fatalf("webapi.New: %v", err)
	}
	gx, err := galaxy.New(hx, store)
	if err != nil {
		t.Fatalf("galaxy.New: %v", err)
	}
	return &Downloader{cfg: cfg, ui: ui, http: hx, web: web, galaxy: gx, progress: deps.Progress, token: store}
}

// TestTransportOwnedByCallerPersistsCookies locks the ownership rule: the
// transport is created (and therefore owned) by the caller, and the jar that
// carried a session cookie is the one SaveCookies writes and LoadCookies
// restores.
//
// Coverage boundary: the cookie is set by a direct request rather than "during a
// webapi call", because webapi's endpoints have no injection point. The property
// being tested — one transport, one jar, one persistence path — is what the
// ownership change guarantees.
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

	cookieFile := filepath.Join(t.TempDir(), "cookies.bin")

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

// TestCredentialsPromptOnDemand locks the credentialed prompting rule: only the
// values that were not supplied are asked for, with --browser-login asking for
// nothing at all. The prompts themselves go to ErrOut — the CLI console owns
// that wording — so this test asserts what was asked for and that stdout stayed
// empty.
func TestCredentialsPromptOnDemand(t *testing.T) {
	cases := []struct {
		name         string
		interactive  bool
		email        string
		forceBrowser bool
		answers      []string
		want         [2]string
		wantPrompts  []string
	}{
		{
			name: "email flag only", interactive: true, email: "a@b", answers: []string{"pw"},
			want: [2]string{"a@b", "pw"}, wantPrompts: []string{"password"},
		},
		{
			name: "no flags", interactive: true, answers: []string{"a@b", "pw"},
			want: [2]string{"a@b", "pw"}, wantPrompts: []string{"email", "password"},
		},
		{
			// --browser-login needs no credentials.
			name: "browser login asks nothing", interactive: true, forceBrowser: true,
			want: [2]string{"", ""},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ui := newFakeConsole(c.answers...)
			cfg := config.NewConfig("/cfg", "/cache")
			cfg.Email = c.email
			cfg.ForceBrowserLogin = c.forceBrowser

			email, password, err := credentials(cfg, ui, c.interactive)
			if err != nil {
				t.Fatalf("credentials: %v", err)
			}
			if email != c.want[0] || password != c.want[1] {
				t.Errorf("credentials = %q/%q, want %q/%q", email, password, c.want[0], c.want[1])
			}
			if got := strings.Join(ui.prompted, ","); got != strings.Join(c.wantPrompts, ",") {
				t.Errorf("prompted %q, want %q", got, strings.Join(c.wantPrompts, ","))
			}
			if ui.out.Len() != 0 {
				t.Errorf("stdout = %q, want empty: prompts do not write program output", ui.out.String())
			}
		})
	}
}

// TestCredentialsEmptyValuesReported covers the failure for a prompt answered
// with an empty line: the value is missing, so the login is not attempted.
func TestCredentialsEmptyValuesReported(t *testing.T) {
	ui := newFakeConsole("", "")
	cfg := config.NewConfig("/cfg", "/cache")

	_, _, err := credentials(cfg, ui, true)
	if err == nil || err.Error() != "Email and/or password empty" {
		t.Fatalf("credentials err = %v, want the empty-credentials message", err)
	}
	if ui.out.Len() != 0 {
		t.Errorf("stdout = %q, want empty", ui.out.String())
	}
}

// TestCredentialsHeadless locks the non-terminal branch: the two persistence
// paths go to stdout, nothing is prompted, the failure text is the same whether
// or not those files exist, and an empty credential pair is never posted — the
// branch fails instead. credentials performs no HTTP work at
// all, so "nothing was posted" is structural rather than merely observed here.
func TestCredentialsHeadless(t *testing.T) {
	newCfg := func(t *testing.T) (config.Config, string, string) {
		t.Helper()
		dir := t.TempDir()
		cfg := config.NewConfig(dir, dir)
		return cfg, cfg.Curl.CookiePath, auth.StorePath(cfg)
	}

	// run drives the branch and returns everything the caller can observe.
	runHeadless := func(t *testing.T, cfg config.Config) (err error, stdout, stderr string) {
		t.Helper()
		ui := newFakeConsole()
		_, _, err = credentials(cfg, ui, false)
		return err, ui.out.String(), ui.errOut.String()
	}

	t.Run("stores missing", func(t *testing.T) {
		cfg, cookieFile, tokenFile := newCfg(t)
		err, out, errOut := runHeadless(t, cfg)

		if err == nil || err.Error() != headlessMessage {
			t.Fatalf("err = %v, want %q", err, headlessMessage)
		}
		if want := cookieFile + "\n" + tokenFile + "\n"; out != want {
			t.Errorf("stdout = %q, want the two store paths %q", out, want)
		}
		if errOut != "" {
			t.Errorf("stderr = %q, want no prompt in a non-interactive session", errOut)
		}
	})

	t.Run("stores present, same message", func(t *testing.T) {
		// The presence of the stores does not change the message —
		// what the caller has to do is identical, and the behavioural
		// difference (no empty-credential request) is in the implementation.
		cfg, cookieFile, tokenFile := newCfg(t)
		if err := os.MkdirAll(cfg.ConfigDirectory, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(cookieFile, []byte("c"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(tokenFile, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
		err, out, errOut := runHeadless(t, cfg)

		if err == nil || err.Error() != headlessMessage {
			t.Fatalf("err = %v, want the unified %q", err, headlessMessage)
		}
		if want := cookieFile + "\n" + tokenFile + "\n"; out != want {
			t.Errorf("stdout = %q, want the two store paths %q", out, want)
		}
		if errOut != "" {
			t.Errorf("stderr = %q, want no prompt", errOut)
		}
	})

	t.Run("--login without a terminal", func(t *testing.T) {
		// The headless branch is a property of the input, not of the flag:
		// --login (bLogin) makes the front end ASK for a login, it cannot make
		// a non-interactive session answer the prompts.
		cfg, _, _ := newCfg(t)
		cfg.Login = true
		err, _, errOut := runHeadless(t, cfg)

		if err == nil || err.Error() != headlessMessage {
			t.Fatalf("err = %v, want %q", err, headlessMessage)
		}
		if errOut != "" {
			t.Errorf("stderr = %q, want no prompt: piped credentials must not be read", errOut)
		}
	})

	t.Run("email only falls through to the branch", func(t *testing.T) {
		// A configured email cannot pair with a password that was never asked
		// for: without a terminal the flow must not become a password-less
		// login.
		cfg, _, _ := newCfg(t)
		cfg.Email = "a@b"
		err, out, errOut := runHeadless(t, cfg)

		if err == nil || err.Error() != headlessMessage {
			t.Fatalf("err = %v, want %q", err, headlessMessage)
		}
		if errOut != "" {
			t.Errorf("stderr = %q, want no prompt", errOut)
		}
		if !strings.Contains(out, auth.StorePath(cfg)) {
			t.Errorf("stdout = %q, want the token path", out)
		}
	})
}

// headlessMessage is the single non-interactive failure text: both sub-cases
// report it, and the hint points at the login command and the terminal it needs
// rather than at login flags this CLI does not have.
const headlessMessage = "no credentials available in a non-interactive session; " +
	"run `goggo auth login` in a terminal"

// TestLoginStreamsAndStatus drives the login flow offline and locks the R2
// stream policy: every status line goes to stderr, stdout stays empty, and the
// challenge reaches the console — which is what selects the wording the CLI
// prints.
//
// Coverage boundary: the double hands httpx a
// caller-provided HTTPClient, and httpx deliberately refuses cookie persistence
// in that configuration, so the flow ends by reporting exactly that error.
// Everything this step guarantees happens before it — the final assertion pins
// the distinction, because reaching the cookie error means the login itself
// completed (token exchange + account probe).
func TestLoginStreamsAndStatus(t *testing.T) {
	cases := []struct {
		name        string
		mode        string
		browser     bool
		answers     []string
		wantKind    webapi.ChallengeKind
		wantCodeLen int
	}{
		{name: "two-step code", mode: "two-step", answers: []string{"1234"},
			wantKind: webapi.ChallengeTwoFactor, wantCodeLen: 4},
		{name: "authenticator code", mode: "totp", answers: []string{"123456"},
			wantKind: webapi.ChallengeTwoFactor, wantCodeLen: 6},
		{name: "browser callback", mode: "plain", browser: true,
			answers:  []string{"https://auth.gog.com/callback?code=pasted"},
			wantKind: webapi.ChallengeBrowser},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := loginTestServer(t, c.mode)
			defer srv.Close()

			cfg := config.NewConfig(t.TempDir(), t.TempDir())
			cfg.Email = "user@example.com"
			cfg.ForceBrowserLogin = c.browser
			// The credential file lives under the configuration directory, which Open
			// normally creates; this test drives login directly.
			if err := os.MkdirAll(cfg.ConfigDirectory, 0o700); err != nil {
				t.Fatal(err)
			}

			// The password is asked for first — the one credential the
			// configuration cannot supply. --browser-login asks for nothing.
			answers := c.answers
			if !c.browser {
				answers = append([]string{"pw"}, c.answers...)
			}
			ui := newFakeConsole(answers...)
			d := newOfflineDownloader(t, srv, cfg, ui)

			err := d.Login(context.Background())
			// The login itself must have completed: core's own evidence is the
			// resolved challenge and the status lines. The sentinel below is
			// httpx's policy for a caller-supplied HTTPClient (pinned in
			// httpx/cookiefile_test.go) and is asserted only to keep the failure
			// mode explicit if it changes.
			if len(ui.challenges) != 1 {
				t.Fatalf("challenges = %d, want exactly one resolved challenge (the login ran)", len(ui.challenges))
			}
			if !strings.Contains(ui.errOut.String(), "Login successful") {
				t.Errorf("stderr = %q, want the login status lines", ui.errOut.String())
			}
			if err != nil && !errors.Is(err, httpx.ErrCookieFileNotConfigured) {
				t.Fatalf("login err = %v, want nil or the double's cookie limitation", err)
			}
			if ui.out.Len() != 0 {
				t.Errorf("stdout = %q, want empty: status lines belong on stderr", ui.out.String())
			}
			if len(ui.challenges) != 1 {
				t.Fatalf("challenges = %d, want exactly one resolved challenge", len(ui.challenges))
			}
			if got := ui.challenges[0].Kind; got != c.wantKind {
				t.Errorf("challenge kind = %v, want %v", got, c.wantKind)
			}
			if ui.challenges[0].CodeLength != c.wantCodeLen {
				t.Errorf("challenge code length = %d, want %d",
					ui.challenges[0].CodeLength, c.wantCodeLen)
			}
			for _, want := range []string{"Galaxy: Login successful", "HTTP: Login successful"} {
				if !strings.Contains(ui.errOut.String(), want) {
					t.Errorf("stderr %q must contain %q", ui.errOut.String(), want)
				}
			}
			if !strings.HasSuffix(ui.errOut.String(), "HTTP: Login successful\n") {
				t.Errorf("stderr = %q, want it to end with the success line", ui.errOut.String())
			}
		})
	}
}

// TestLoginFailureReportsOnce locks that the status output must not
// double-report: on the failure path login writes no status line at all, and the
// reason plus its label travel in a SINGLE error chain that the front end
// renders as exactly one `Error: …` line.
func TestLoginFailureReportsOnce(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth":
			fmt.Fprint(w, `<html><body><form><input name="login[_token]" value="tok"></form></body></html>`)
		case "/login_check":
			// The callback carries no auth code and the page has no reCAPTCHA
			// marker, so the form login yields nothing and Login fails.
			http.Redirect(w, r, "/callback", http.StatusFound)
		case "/callback":
			fmt.Fprint(w, "callback")
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	cfg := config.NewConfig(t.TempDir(), t.TempDir())
	cfg.Email = "user@example.com"
	ui := newFakeConsole("pw")
	d := newOfflineDownloader(t, srv, cfg, ui)

	err := d.Login(context.Background())
	if err == nil {
		t.Fatal("login must fail when the server returns no auth code")
	}
	if !strings.HasPrefix(err.Error(), "Galaxy: Login failed: ") {
		t.Errorf("error = %q, want the label at the head of one error chain", err.Error())
	}
	if got := strings.Count(err.Error(), "Login failed"); got != 1 {
		t.Errorf("error = %q mentions the failure %d times, want exactly one", err.Error(), got)
	}
	if strings.Contains(err.Error(), "Login successful") {
		t.Errorf("error = %q must not carry a success status", err.Error())
	}
	if ui.errOut.Len() != 0 || ui.out.Len() != 0 {
		t.Errorf("failure path wrote stderr %q / stdout %q, want nothing: the error chain is the report",
			ui.errOut.String(), ui.out.String())
	}
}

// TestEnsureDirectories locks the startup directory creation that the first
// real login proved was missing (token persistence failed on a non-existent
// %AppData%\goggo).
func TestEnsureDirectories(t *testing.T) {
	root := t.TempDir()
	cfg := config.NewConfig(filepath.Join(root, "conf"), filepath.Join(root, "cache"))
	if err := ensureDirectories(cfg); err != nil {
		t.Fatalf("ensureDirectories: %v", err)
	}
	for _, dir := range []string{cfg.ConfigDirectory, cfg.CacheDirectory, cfg.XMLDirectory} {
		fi, err := os.Stat(dir)
		if err != nil || !fi.IsDir() {
			t.Errorf("directory %q not created: %v", dir, err)
		}
	}
	// Idempotent: running it again must succeed.
	if err := ensureDirectories(cfg); err != nil {
		t.Errorf("second ensureDirectories: %v", err)
	}

	// A configuration root whose parent is a regular file cannot be created.
	blocker := filepath.Join(root, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	bad := config.NewConfig(blocker, filepath.Join(root, "cache2"))
	err := ensureDirectories(bad)
	if err == nil {
		t.Fatal("ensureDirectories must fail when a parent path is a file")
	}
	// The wrapped error names the directory it tried to create; compare on the
	// caller's root so the assertion stays platform-independent (the joined path
	// uses forward slashes while filepath.Join would not).
	if !strings.Contains(err.Error(), blocker) {
		t.Errorf("error %q should carry the directory path", err)
	}
}

// TestInitRefreshesAndSavesExpiredToken covers the boundary:
// load, notice the token is expired, refresh it and save the result — without
// binding to the credential file format. The store is seeded through the production
// writer and the result is read back through the production reader, so nothing
// here depends on how the file is laid out.
//
// Coverage boundary: the sequence runs through the store Open builds plus
// Downloader.Init rather than through Open, because Open builds its transport
// from the configuration and therefore cannot be pointed at a test server. Open's
// own glue is not exercised here.
func TestInitRefreshesAndSavesExpiredToken(t *testing.T) {
	var tokenRequests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			tokenRequests++
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"access_token":"at-2","refresh_token":"rt-2","expires_in":3600,"user_id":"u1"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	cfg := config.NewConfig(t.TempDir(), t.TempDir())
	// Open creates these before it touches the token store; the test drives the
	// same steps explicitly.
	if err := ensureDirectories(cfg); err != nil {
		t.Fatalf("ensureDirectories: %v", err)
	}
	seed, err := auth.Open(auth.StorePath(cfg))
	if err != nil {
		t.Fatalf("auth.Open: %v", err)
	}
	seed.StoreLoginResponse(map[string]any{
		"access_token": "at-1", "refresh_token": "rt-1", "expires_in": -10, "user_id": "u1",
	})
	if err := seed.Save(); err != nil {
		t.Fatalf("seed the token store: %v", err)
	}

	// The downloader opens the same location, so the seeded store is what it
	// starts from.
	d := newOfflineDownloader(t, srv, cfg, newFakeConsole())
	if !d.token.Expired() {
		t.Fatal("the seeded token must read as expired")
	}

	if err := d.Init(context.Background()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if tokenRequests != 1 {
		t.Errorf("token endpoint was called %d times, want exactly one refresh", tokenRequests)
	}
	if got := d.token.AuthorizationValue(); got != "Bearer at-2" {
		t.Errorf("stored authorization value = %q, want the refreshed token", got)
	}

	// Saved through the production reader: a fresh store sees the new token.
	reread, err := auth.Open(auth.StorePath(cfg))
	if err != nil {
		t.Fatalf("auth.Open after Init: %v", err)
	}
	if got := reread.AuthorizationValue(); got != "Bearer at-2" {
		t.Errorf("stored authorization value = %q, want the refreshed token written to disk", got)
	}
}

// TestInitWithoutUsableTokenReportsFailure locks the contract that a failed
// initialisation stops the run.
func TestInitWithoutUsableTokenReportsFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := config.NewConfig(t.TempDir(), t.TempDir())
	d := newOfflineDownloader(t, srv, cfg, newFakeConsole())

	err := d.Init(context.Background())
	if err == nil {
		t.Fatal("Init must fail when there is no usable token and the refresh fails")
	}
	if !errors.Is(err, errNoToken) {
		t.Errorf("error = %v, want it to wrap errNoToken", err)
	}
}

// openTestServer models the endpoints Open touches and counts each of them, so a
// test can tell "the login flow never ran" from "it ran": the account probe
// decides LoggedIn, the token endpoint is the refresh, and the login form is the
// only path that ends in a cookie flush.
type openTestServer struct {
	*httptest.Server

	mu       sync.Mutex
	accounts int
	tokens   int
	logins   int
}

func newOpenTestServer(t *testing.T) *openTestServer {
	t.Helper()
	s := &openTestServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		switch r.URL.Path {
		case "/www/account":
			s.accounts++
			fmt.Fprint(w, "account")
		case "/token":
			s.tokens++
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"access_token":"at-new","refresh_token":"rt-new","expires_in":3600,"user_id":"u1"}`)
		case "/auth":
			fmt.Fprint(w, `<html><body><form><input name="login[_token]" value="tok"></form></body></html>`)
		case "/login_check":
			s.logins++
			http.Redirect(w, r, "/callback?code=plain-code", http.StatusFound)
		case "/callback":
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "jar-cookie", Path: "/"})
			fmt.Fprint(w, "callback")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *openTestServer) counts() (accounts, tokens, logins int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.accounts, s.tokens, s.logins
}

// injectedDeps is the seam under test: only the network exit changes, so the
// production cookie file, retry policy and low-speed guard stay in place.
func injectedDeps(t *testing.T, srv *httptest.Server) Dependencies {
	t.Helper()
	target, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	return Dependencies{HTTPTransport: &gogHostTransport{target: target}}
}

// seededToken writes a token store through the production writer, so the tests
// do not depend on the file format.
func seededToken(t *testing.T, cfg config.Config, expiresIn int) {
	t.Helper()
	if err := ensureDirectories(cfg); err != nil {
		t.Fatalf("ensureDirectories: %v", err)
	}
	store, err := auth.Open(auth.StorePath(cfg))
	if err != nil {
		t.Fatalf("auth.Open: %v", err)
	}
	store.StoreLoginResponse(map[string]any{
		"access_token": "at-seed", "refresh_token": "rt-seed",
		"expires_in": expiresIn, "user_id": "u1",
	})
	if err := store.Save(); err != nil {
		t.Fatalf("seed the token store: %v", err)
	}
}

// TestOpenWithInjectedTransportSeesAFreshAccount covers the seam's happy path
// and the reason it replaces only the network exit: the run keeps its cookie
// file, so the session it builds persists like any other.
func TestOpenWithInjectedTransportSeesAFreshAccount(t *testing.T) {
	srv := newOpenTestServer(t)
	cfg := config.NewConfig(t.TempDir(), t.TempDir())
	seededToken(t, cfg, 3600)

	d, err := OpenWith(context.Background(), cfg, newFakeConsole(), SessionRequest{}, injectedDeps(t, srv.Server))
	if err != nil {
		t.Fatalf("OpenWith: %v", err)
	}
	if !d.LoggedIn() {
		t.Error("LoggedIn = false, want true: a fresh token and a 200 account probe are the logged-in state")
	}
	accounts, tokens, logins := srv.counts()
	if accounts != 1 {
		t.Errorf("account probes = %d, want exactly one", accounts)
	}
	if tokens != 0 {
		t.Errorf("token requests = %d, want none: the seeded token is fresh", tokens)
	}
	if logins != 0 {
		t.Errorf("login attempts = %d, want none", logins)
	}

	// The cookie file is the production path, and Close flushes into it: this is
	// the property that a client-injecting seam would have lost.
	if err := d.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Stat(cfg.Curl.CookiePath); err != nil {
		t.Errorf("cookie file %q was not written: %v", cfg.Curl.CookiePath, err)
	}
}

// TestOpenWithInjectedTransportRefreshesExpiredToken covers the refresh branch
// through Open itself.
func TestOpenWithInjectedTransportRefreshesExpiredToken(t *testing.T) {
	srv := newOpenTestServer(t)
	cfg := config.NewConfig(t.TempDir(), t.TempDir())
	seededToken(t, cfg, -10)

	d, err := OpenWith(context.Background(), cfg, newFakeConsole(), SessionRequest{}, injectedDeps(t, srv.Server))
	if err != nil {
		t.Fatalf("OpenWith: %v", err)
	}
	_, tokens, logins := srv.counts()
	if tokens != 1 {
		t.Errorf("token requests = %d, want exactly one refresh", tokens)
	}
	if logins != 0 {
		t.Errorf("login attempts = %d, want none: the refresh succeeded", logins)
	}
	if got := d.token.AuthorizationValue(); got != "Bearer at-new" {
		t.Errorf("stored authorization value = %q, want the refreshed one", got)
	}

	// Saved through the production reader: a fresh store sees the new token.
	reread, err := auth.Open(auth.StorePath(cfg))
	if err != nil {
		t.Fatalf("auth.Open after OpenWith: %v", err)
	}
	if got := reread.AuthorizationValue(); got != "Bearer at-new" {
		t.Errorf("stored authorization value = %q, want the refreshed token written to disk", got)
	}
}

// TestOpenWithInjectedTransportWithoutLoginPermission covers the
// --check-login-status path: with the zero request — what `auth status` asks for
// — a not-logged-in account is reported, never logged in and never failed.
func TestOpenWithInjectedTransportWithoutLoginPermission(t *testing.T) {
	srv := newOpenTestServer(t)
	cfg := config.NewConfig(t.TempDir(), t.TempDir())

	d, err := OpenWith(context.Background(), cfg, newFakeConsole(), SessionRequest{}, injectedDeps(t, srv.Server))
	if err != nil {
		t.Fatalf("OpenWith: %v", err)
	}
	if d.LoggedIn() {
		t.Error("LoggedIn = true, want false: the account probe succeeded but no usable token exists")
	}
	accounts, tokens, logins := srv.counts()
	if accounts != 1 {
		t.Errorf("account probes = %d, want exactly one", accounts)
	}
	if tokens != 0 || logins != 0 {
		t.Errorf("token requests = %d and login attempts = %d, want none", tokens, logins)
	}
}

// TestOpenWithInjectedTransportWithoutToken covers a fresh install: no token
// file at all is not an error — the store is simply left empty.
func TestOpenWithInjectedTransportWithoutToken(t *testing.T) {
	srv := newOpenTestServer(t)
	cfg := config.NewConfig(t.TempDir(), t.TempDir())

	d, err := OpenWith(context.Background(), cfg, newFakeConsole(), SessionRequest{}, injectedDeps(t, srv.Server))
	if err != nil {
		t.Fatalf("OpenWith without a credential file: %v", err)
	}
	if d.LoggedIn() {
		t.Error("LoggedIn = true, want false without a token")
	}
}

// TestOpenWithInjectedTransportRunsTheFullLogin locks the property the seam
// must preserve: a run through the seam can complete a login AND flush its
// cookie jar, because the seam changes the network exit only.
func TestOpenWithInjectedTransportRunsTheFullLogin(t *testing.T) {
	srv := newOpenTestServer(t)
	cfg := config.NewConfig(t.TempDir(), t.TempDir())
	cfg.Email = "user@example.com"

	d, err := OpenWith(context.Background(), cfg, newFakeConsole("pw"), SessionRequest{AllowLogin: true}, injectedDeps(t, srv.Server))
	if err != nil {
		t.Fatalf("OpenWith with a login: %v", err)
	}
	if !d.LoggedIn() {
		t.Error("LoggedIn = false after a completed login")
	}
	accounts, tokens, logins := srv.counts()
	if logins != 1 || tokens != 1 {
		t.Errorf("login attempts = %d, token requests = %d; want one of each", logins, tokens)
	}
	if accounts < 1 {
		t.Errorf("account probes = %d, want the post-login probe to have run", accounts)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	for _, path := range []string{cfg.Curl.CookiePath, auth.StorePath(cfg)} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%q was not written: %v", path, err)
		}
	}
}

// TestSessionRequestReadOnlyNeverLogsIn locks the read-only half of the login
// contract: a command that needs a session and may not create
// one fails with the one actionable error, without prompting and without the
// login flow — even though this console could answer, which is what makes the
// refusal policy rather than a missing terminal.
func TestSessionRequestReadOnlyNeverLogsIn(t *testing.T) {
	srv := newOpenTestServer(t)
	cfg := config.NewConfig(t.TempDir(), t.TempDir())
	ui := newFakeConsole() // a terminal, with no answers: any prompt would fail

	d, err := OpenWith(context.Background(), cfg, ui, SessionRequest{Required: true},
		injectedDeps(t, srv.Server))
	if !errors.Is(err, ErrSessionRequired) {
		t.Fatalf("err = %v, want ErrSessionRequired", err)
	}
	if want := "not logged in; run `goggo auth login`"; err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
	if d != nil {
		t.Error("a refused run must not hand back a Downloader")
	}
	if got := strings.Join(ui.prompted, ","); got != "" {
		t.Errorf("prompted %q, want nothing: a read-only command may not ask for credentials", got)
	}
	if ui.out.Len() != 0 || ui.errOut.Len() != 0 {
		t.Errorf("wrote %q / %q, want nothing", ui.out.String(), ui.errOut.String())
	}
	accounts, tokens, logins := srv.counts()
	if accounts != 1 {
		t.Errorf("account probes = %d, want exactly one", accounts)
	}
	if tokens != 0 || logins != 0 {
		t.Errorf("token requests = %d and login attempts = %d, want none", tokens, logins)
	}
}

// TestSessionRequestNonInteractiveFailFast locks the writing half on a machine
// that cannot answer. The run stops with the actionable error
// and never reaches the headless credentials branch: that branch's fingerprint
// is the two store paths it prints, and it is written for an explicit login
// only.
func TestSessionRequestNonInteractiveFailFast(t *testing.T) {
	srv := newOpenTestServer(t)
	cfg := config.NewConfig(t.TempDir(), t.TempDir())
	ui := newFakeConsole()
	ui.interactive = false

	d, err := OpenWith(context.Background(), cfg, ui, SessionRequest{Required: true},
		injectedDeps(t, srv.Server))
	if !errors.Is(err, ErrSessionRequired) {
		t.Fatalf("err = %v, want ErrSessionRequired", err)
	}
	if d != nil {
		t.Error("a refused run must not hand back a Downloader")
	}
	if ui.out.Len() != 0 {
		t.Errorf("stdout = %q, want nothing: the headless branch prints the store paths", ui.out.String())
	}
	if ui.errOut.Len() != 0 {
		t.Errorf("stderr = %q, want nothing", ui.errOut.String())
	}
	if _, _, logins := srv.counts(); logins != 0 {
		t.Errorf("login attempts = %d, want none", logins)
	}
}

// TestSessionRequestImplicitLoginOnATerminal covers automatic login: the two
// prompts a missing account needs are asked, the flow completes and the run
// comes back usable.
func TestSessionRequestImplicitLoginOnATerminal(t *testing.T) {
	srv := newOpenTestServer(t)
	cfg := config.NewConfig(t.TempDir(), t.TempDir())
	ui := newFakeConsole("user@example.com", "pw")

	d, err := OpenWith(context.Background(), cfg, ui,
		SessionRequest{Required: true, AllowLogin: true}, injectedDeps(t, srv.Server))
	if err != nil {
		t.Fatalf("OpenWith: %v", err)
	}
	if !d.LoggedIn() {
		t.Error("LoggedIn = false after an automatic login")
	}
	if got := strings.Join(ui.prompted, ","); got != "email,password" {
		t.Errorf("prompted %q, want the email and the password", got)
	}
	_, tokens, logins := srv.counts()
	if logins != 1 || tokens != 1 {
		t.Errorf("login attempts = %d, token requests = %d; want one of each", logins, tokens)
	}
}

// TestSessionRequestExplicitLoginKeepsTheNonInteractiveBranch locks why an
// explicit login is its own class: with no terminal it
// still runs and reports the missing credentials, and a session-requiring
// command reports that real failure rather than ErrSessionRequired — the login
// attempt comes before the requirement is judged.
func TestSessionRequestExplicitLoginKeepsTheNonInteractiveBranch(t *testing.T) {
	srv := newOpenTestServer(t)
	cfg := config.NewConfig(t.TempDir(), t.TempDir())
	ui := newFakeConsole()
	ui.interactive = false

	// What `auth login` asks for: run the flow, need nothing beforehand.
	if _, err := OpenWith(context.Background(), cfg, ui, SessionRequest{AllowLogin: true},
		injectedDeps(t, srv.Server)); err == nil || err.Error() != headlessMessage {
		t.Fatalf("err = %v, want %q", err, headlessMessage)
	}
	if want := cfg.Curl.CookiePath + "\n" + auth.StorePath(cfg) + "\n"; ui.out.String() != want {
		t.Errorf("stdout = %q, want the two store paths %q", ui.out.String(), want)
	}

	ui.out.Reset()
	_, err := OpenWith(context.Background(), cfg, ui,
		SessionRequest{Required: true, AllowLogin: true}, injectedDeps(t, srv.Server))
	if err == nil || errors.Is(err, ErrSessionRequired) {
		t.Fatalf("err = %v, want the login failure rather than the requirement error", err)
	}
	if _, _, logins := srv.counts(); logins != 0 {
		t.Errorf("login attempts = %d, want none: no credentials were ever supplied", logins)
	}
}

// TestRetryWaitIsMilliseconds locks the unit the CLI exposes: --wait counts
// MILLISECONDS, so the wait the client is built with is exactly the configured
// number of milliseconds — the ends of the range included (zero stays zero, a
// large value keeps its magnitude). The wiring that puts that value on the wire
// is TestSessionRetryWaitReachesTheWire's subject.
func TestRetryWaitIsMilliseconds(t *testing.T) {
	for _, tc := range []struct {
		wait int
		want time.Duration
	}{
		{0, 0},
		{1, time.Millisecond},
		{500, 500 * time.Millisecond},
		{2000, 2 * time.Second},
	} {
		cfg := config.Config{Wait: tc.wait}
		if got := retryWait(cfg); got != tc.want {
			t.Errorf("retryWait(--wait %d) = %v, want %v", tc.wait, got, tc.want)
		}
	}
}

// TestSessionRetryWaitReachesTheWire locks the wiring with real timing, not
// just the conversion: the http client core builds (httpxCfg, the same call
// OpenWith makes) must sleep the configured wait between website retries. The
// account endpoints go through getResponse -> GetBytesWithRetry, so a server
// that fails once and then answers makes one retry happen; with --wait 200 the
// elapsed time is ~200ms, while the old microsecond reading elapsed ~0.
func TestSessionRetryWaitReachesTheWire(t *testing.T) {
	// The construction point itself: the policy core hands to httpx. Core owns
	// the mapping from cfg.Wait; the attempt formula (min(retries,3)+1) belongs
	// to webapi.RetryPolicyFor and is pinned in webapi/retrypolicy_test.go.
	cfg := config.Config{Wait: 200, Retries: 3}
	built := httpxCfg(cfg, Dependencies{})
	if built.RetryPolicy.Wait != 200*time.Millisecond {
		t.Fatalf("httpxCfg policy wait = %v, want 200ms", built.RetryPolicy.Wait)
	}
	if built.RetryPolicy.MaxAttempts != cfg.Retries+1 {
		t.Errorf("httpxCfg attempts = %d, want the retries plus the first attempt", built.RetryPolicy.MaxAttempts)
	}

	var mu sync.Mutex
	hits := 0
	wrote := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		first := !wrote
		wrote = true
		mu.Unlock()
		if first {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, "{}")
	}))
	defer srv.Close()

	target, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	cfg2 := config.Config{Wait: 200, Retries: 3}
	hx, err := httpx.New(httpxCfg(cfg2, Dependencies{HTTPTransport: &gogHostTransport{target: target}}))
	if err != nil {
		t.Fatalf("httpx.New: %v", err)
	}
	store, err := auth.Open("")
	if err != nil {
		t.Fatalf("auth.Open: %v", err)
	}
	web, err := webapi.New(hx, store)
	if err != nil {
		t.Fatalf("webapi.New: %v", err)
	}

	start := time.Now()
	if _, err := web.GameDetailsJSON(context.Background(), "1"); err != nil {
		t.Fatalf("GameDetailsJSON: %v", err)
	}
	elapsed := time.Since(start)

	mu.Lock()
	attempts := hits
	mu.Unlock()
	if attempts < 2 {
		t.Fatalf("requests = %d, want the retry to have run", attempts)
	}
	if elapsed < 190*time.Millisecond {
		t.Errorf("elapsed = %v, want at least the configured 200ms wait (a microsecond reading would elapse ~0)", elapsed)
	}
}

// probeFlakyTransport fails the first www.gog.com/account probe and delegates
// the rest: the seam for the state where the probe cannot answer.
type probeFlakyTransport struct {
	base    *gogHostTransport
	mu      sync.Mutex
	pending bool
}

func (t *probeFlakyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.mu.Lock()
	fail := t.pending && req.URL.Host == "www.gog.com" && req.URL.Path == "/account"
	if fail {
		t.pending = false
	}
	t.mu.Unlock()
	if fail {
		return nil, errors.New("fixture: probe transport down")
	}
	return t.base.RoundTrip(req)
}

func flakyProbeDeps(t *testing.T, srv *httptest.Server) Dependencies {
	t.Helper()
	target, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse fixture URL: %v", err)
	}
	return Dependencies{HTTPTransport: &probeFlakyTransport{base: &gogHostTransport{target: target}, pending: true}}
}

// TestSessionProbeFailureIsUnconfirmed locks the tri-state: a probe that could
// not answer must not produce ErrSessionRequired — the credential may be fine
// and the message that says otherwise sends the user to re-login a live
// session.
func TestSessionProbeFailureIsUnconfirmed(t *testing.T) {
	srv := newOpenTestServer(t)
	cfg := config.NewConfig(t.TempDir(), t.TempDir())
	ui := newFakeConsole()

	d, err := OpenWith(context.Background(), cfg, ui, SessionRequest{Required: true},
		flakyProbeDeps(t, srv.Server))
	if err == nil {
		t.Fatal("probe failure must not hand back a run")
	}
	if errors.Is(err, ErrSessionRequired) {
		t.Errorf("err = %v, want the unconfirmed failure, not the missing-session advice", err)
	}
	if !errors.Is(err, ErrSessionUnconfirmed) {
		t.Fatalf("err = %v, want ErrSessionUnconfirmed", err)
	}
	if want := "cannot confirm the login session: "; !strings.HasPrefix(err.Error(), want) {
		t.Errorf("message = %q, want the %q prefix", err.Error(), want)
	}
	if d != nil {
		t.Error("a refused run must not hand back a Downloader")
	}
}

// TestProbeErrorClearedByCompletedLogin locks the lifetime: a login that
// completes through the AllowLogin branch is definite evidence of a session,
// so the stale probe failure must not survive into the status report.
func TestProbeErrorClearedByCompletedLogin(t *testing.T) {
	srv := newOpenTestServer(t)
	cfg := config.NewConfig(t.TempDir(), t.TempDir())
	cfg.Email = "user@example.com"

	d, err := OpenWith(context.Background(), cfg, newFakeConsole("pw"),
		SessionRequest{AllowLogin: true}, flakyProbeDeps(t, srv.Server))
	if err != nil {
		t.Fatalf("OpenWith: %v", err)
	}
	if !d.LoggedIn() {
		t.Fatal("LoggedIn = false after a completed login")
	}
	if got := d.SessionProbeErr(); got != nil {
		t.Errorf("SessionProbeErr = %v, want the completed login to clear it", got)
	}
}
