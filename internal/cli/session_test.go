package cli

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
	"regexp"
	"strings"
	"testing"

	"github.com/nekrozis/goggo/internal/config"
	"github.com/nekrozis/goggo/internal/httpx"
	"github.com/nekrozis/goggo/internal/webapi"
)

// TestTransportOwnedByCallerPersistsCookies locks the S12-R ownership rule: the
// transport is created (and therefore owned) by the caller, and the jar that
// carried a session cookie is the one SaveCookies writes and LoadCookies
// restores.
//
// Coverage boundary (recorded in the audit): the cookie is set by a direct
// request rather than "during a webapi call", because webapi's endpoints have
// no injection point. The property being tested — one transport, one jar, one
// persistence path — is what the ownership change guarantees; the end-to-end
// form is verified by GATE-A.
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

	cookieFile := filepath.Join(t.TempDir(), "cookies.txt")

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
// values that were not supplied are asked for (intentional difference from the
// C++ behaviour of prompting for both unless both flags are set), with
// --browser-login asking for nothing at all. Every prompt must go to stderr so
// stdout stays program output.
func TestCredentialsPromptOnDemand(t *testing.T) {
	cases := []struct {
		name         string
		interactive  bool
		email        string
		password     string
		forceBrowser bool
		stdin        string
		want         [2]string
		wantErrOut   []string
		denyErrOut   []string
	}{
		{
			name: "email flag only", interactive: true, email: "a@b", stdin: "pw\n",
			want:       [2]string{"a@b", "pw"},
			wantErrOut: []string{"Password: "},
			denyErrOut: []string{"Email: "},
		},
		{
			name: "both flags", interactive: true, email: "a@b", password: "pw",
			want:       [2]string{"a@b", "pw"},
			denyErrOut: []string{"Email: ", "Password: "},
		},
		{
			name: "no flags", interactive: true, stdin: "a@b\npw\n",
			want:       [2]string{"a@b", "pw"},
			wantErrOut: []string{"Email: ", "Password: "},
		},
		{
			// --browser-login needs no credentials (downloader.cpp:254).
			name: "browser login asks nothing", interactive: true, forceBrowser: true,
			want:       [2]string{"", ""},
			denyErrOut: []string{"Email: ", "Password: "},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			ui := newConsole(strings.NewReader(c.stdin), &out, &errOut)
			cfg := config.NewConfig("/cfg", "/cache")
			cfg.Email, cfg.Password = c.email, c.password
			cfg.ForceBrowserLogin = c.forceBrowser

			email, password, err := credentials(cfg, ui, c.interactive)
			if err != nil {
				t.Fatalf("credentials: %v", err)
			}
			if email != c.want[0] || password != c.want[1] {
				t.Errorf("credentials = %q/%q, want %q/%q", email, password, c.want[0], c.want[1])
			}
			for _, want := range c.wantErrOut {
				if !strings.Contains(errOut.String(), want) {
					t.Errorf("prompt output %q must contain %q", errOut.String(), want)
				}
			}
			for _, deny := range c.denyErrOut {
				if strings.Contains(errOut.String(), deny) {
					t.Errorf("prompt output %q must not contain %q", errOut.String(), deny)
				}
			}
			if out.Len() != 0 {
				t.Errorf("stdout = %q, want empty: prompts belong on stderr", out.String())
			}
		})
	}
}

// TestCredentialsEmptyValuesReported covers the upstream failure for a prompt
// answered with an empty line (downloader.cpp:282-288): the value is missing,
// so the login is not attempted.
func TestCredentialsEmptyValuesReported(t *testing.T) {
	var out, errOut bytes.Buffer
	ui := newConsole(strings.NewReader("\n\n"), &out, &errOut)
	cfg := config.NewConfig("/cfg", "/cache")

	_, _, err := credentials(cfg, ui, true)
	if err == nil || err.Error() != "Email and/or password empty" {
		t.Fatalf("credentials err = %v, want the upstream empty-credentials message", err)
	}
	if out.Len() != 0 {
		t.Errorf("stdout = %q, want empty", out.String())
	}
}

// headlessMessage is the single non-interactive failure text: review ruling ①
// unified the two sub-cases, and ② made the hint actionable by naming the flags
// (--login cannot prompt in a non-interactive session either).
const headlessMessage = "no credentials available in a non-interactive session; " +
	"run --login in a terminal, or pass --login-email/--login-password"

// TestCredentialsHeadless locks the non-terminal branch (downloader.cpp:256-265)
// against review rulings Q1=b, ① and ②: the two persistence paths go to stdout,
// nothing is prompted, the failure text is the same whether or not those files
// exist, and an empty credential pair is never posted — the branch fails
// instead. credentials performs no HTTP work at all, so "nothing was posted" is
// structural rather than merely observed here.
func TestCredentialsHeadless(t *testing.T) {
	newCfg := func(t *testing.T) (config.Config, string, string) {
		t.Helper()
		dir := t.TempDir()
		cfg := config.NewConfig(dir, dir)
		return cfg, cfg.Curl.CookiePath, tokenPath(cfg)
	}

	// run drives the branch and returns everything the caller can observe.
	runHeadless := func(t *testing.T, cfg config.Config, stdin string) (err error, stdout, stderr string) {
		t.Helper()
		var out, errOut bytes.Buffer
		ui := newConsole(strings.NewReader(stdin), &out, &errOut)
		_, _, err = credentials(cfg, ui, false)
		return err, out.String(), errOut.String()
	}

	t.Run("stores missing", func(t *testing.T) {
		cfg, cookieFile, tokenFile := newCfg(t)
		err, out, errOut := runHeadless(t, cfg, "")

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
		// Ruling ①: the presence of the stores does not change the message —
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
		err, out, errOut := runHeadless(t, cfg, "")

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
		err, _, errOut := runHeadless(t, cfg, "a@b\npw\n")

		if err == nil || err.Error() != headlessMessage {
			t.Fatalf("err = %v, want %q", err, headlessMessage)
		}
		if errOut != "" {
			t.Errorf("stderr = %q, want no prompt: the piped credentials must not be read", errOut)
		}
	})

	t.Run("email only falls through to the branch", func(t *testing.T) {
		// downloader.cpp:249 tests the PAIR first, so a lone --login-email does
		// not become a password-less login.
		cfg, _, _ := newCfg(t)
		cfg.Email = "a@b"
		err, out, errOut := runHeadless(t, cfg, "pw\n")

		if err == nil || err.Error() != headlessMessage {
			t.Fatalf("err = %v, want %q", err, headlessMessage)
		}
		if errOut != "" {
			t.Errorf("stderr = %q, want no prompt", errOut)
		}
		if !strings.Contains(out, tokenPath(cfg)) {
			t.Errorf("stdout = %q, want the token path", out)
		}
	})

	t.Run("supplied pair skips the branch", func(t *testing.T) {
		cfg, _, _ := newCfg(t)
		cfg.Email, cfg.Password = "a@b", "pw"
		var out, errOut bytes.Buffer
		ui := newConsole(strings.NewReader(""), &out, &errOut)

		email, password, err := credentials(cfg, ui, false)
		if err != nil {
			t.Fatalf("credentials: %v", err)
		}
		if email != "a@b" || password != "pw" {
			t.Errorf("credentials = %q/%q, want the supplied pair", email, password)
		}
		if out.Len() != 0 || errOut.Len() != 0 {
			t.Errorf("output = %q / %q, want nothing (downloader.cpp:249-253 takes the pair first)",
				out.String(), errOut.String())
		}
	})
}

// TestPromptPasswordWithoutTerminal covers the injected-reader branch: with no
// terminal to hide behind the line is read normally, which is what keeps the
// front end testable. The hidden path (term.ReadPassword) needs a real console
// and is verified by the GATE-A run on Windows.
func TestPromptPasswordWithoutTerminal(t *testing.T) {
	var out, errOut bytes.Buffer
	ui := newConsole(strings.NewReader("secret\n"), &out, &errOut)
	got, err := ui.promptPassword()
	if err != nil {
		t.Fatalf("promptPassword: %v", err)
	}
	if got != "secret" {
		t.Errorf("password = %q, want %q", got, "secret")
	}
	if !strings.Contains(errOut.String(), "Password: ") {
		t.Errorf("prompt output = %q, want it on stderr", errOut.String())
	}
	if out.Len() != 0 {
		t.Errorf("stdout = %q, want empty", out.String())
	}
}

// gogHostTransport maps the production GOG hosts onto a test server so the login
// flow can be driven offline. Only the transport is doubled: webapi builds the
// production URLs itself, and what is under test is where the front end writes
// its prompts and status lines.
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

// newOfflineSession builds a Session whose transport is the rewriting double.
func newOfflineSession(t *testing.T, srv *httptest.Server, cfg config.Config) *Session {
	t.Helper()
	target, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	hx, err := httpx.New(httpx.Config{HTTPClient: &http.Client{Transport: &gogHostTransport{target: target}}})
	if err != nil {
		t.Fatalf("httpx.New: %v", err)
	}
	galaxy := config.NewGalaxyConfig()
	web, err := webapi.New(hx, galaxy)
	if err != nil {
		t.Fatalf("webapi.New: %v", err)
	}
	galaxy.SetFilepath(tokenPath(cfg))
	return &Session{Config: cfg, HTTP: hx, Web: web, Galaxy: galaxy}
}

// TestLoginStreamsAndStatus drives the login flow offline and locks the R2
// stream policy: every prompt and both Galaxy/HTTP status lines go to stderr,
// stdout stays empty, and the wording is the upstream one.
//
// Coverage boundary (recorded in the audit): the double hands httpx a
// caller-provided HTTPClient, and httpx deliberately refuses cookie persistence
// in that configuration, so the flow ends by reporting exactly that error.
// Everything this step guarantees happens before it — the final assertion pins
// the distinction, because reaching the cookie error means the login itself
// completed (token exchange + account probe).
func TestLoginStreamsAndStatus(t *testing.T) {
	cases := []struct {
		name             string
		mode             string
		browser          bool
		stdin            string
		wantPromptPrefix string
		wantBrowserBlock bool
		wantErrOut       []string
	}{
		{
			name: "two-step code", mode: "two-step", stdin: "1234\n",
			wantPromptPrefix: "Security code: ",
			wantErrOut:       []string{"Galaxy: Login successful", "HTTP: Login successful"},
		},
		{
			// The 6-character authenticator code is asked for differently
			// (website.cpp:521-525); webapi exposes only the length, so this
			// case is what pins the wording branch.
			name: "authenticator code", mode: "totp", stdin: "123456\n",
			wantPromptPrefix: "Authenticator security code: ",
			wantErrOut:       []string{"Galaxy: Login successful", "HTTP: Login successful"},
		},
		{
			name: "browser callback", mode: "plain", browser: true,
			stdin:            "https://auth.gog.com/callback?code=pasted\n",
			wantPromptPrefix: "Login using browser at the following url\n",
			wantBrowserBlock: true,
			wantErrOut: []string{
				"Galaxy: Login successful",
				"HTTP: Login successful",
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := loginTestServer(t, c.mode)
			defer srv.Close()

			cfg := config.NewConfig(t.TempDir(), t.TempDir())
			cfg.Email, cfg.Password = "user@example.com", "pw"
			cfg.ForceBrowserLogin = c.browser
			// The token file lives under the configuration directory, which the
			// session normally creates in Open; this test drives login directly.
			if err := os.MkdirAll(cfg.ConfigDirectory, 0o700); err != nil {
				t.Fatal(err)
			}

			var out, errOut bytes.Buffer
			ui := newConsole(strings.NewReader(c.stdin), &out, &errOut)
			s := newOfflineSession(t, srv, cfg)

			err := s.login(context.Background(), ui)
			if !errors.Is(err, httpx.ErrCookieFileNotConfigured) {
				t.Fatalf("login err = %v, want the cookie-persistence limitation of the double "+
					"(reaching it means the login itself succeeded)", err)
			}
			if out.Len() != 0 {
				t.Errorf("stdout = %q, want empty: prompts and status belong on stderr", out.String())
			}
			if c.wantPromptPrefix != "" && !strings.HasPrefix(errOut.String(), c.wantPromptPrefix) {
				t.Errorf("stderr must start with the prompt %q, got %q", c.wantPromptPrefix, errOut.String())
			}
			if c.wantBrowserBlock {
				// The whole block, in order: the URL line, the blank line after
				// it, then the paste instruction and the `URL: ` prompt
				// (website.cpp:614-617). A partial move would fail here.
				block := regexp.MustCompile(`^Login using browser at the following url\n` +
					`https://auth\.gog\.com/auth\?[^\n]+\n\n` +
					`Copy & paste the full url from your browser here after login is complete\n` +
					`URL: `)
				if !block.MatchString(errOut.String()) {
					t.Errorf("browser block is not the ordered upstream block: %q", errOut.String())
				}
			}
			for _, want := range c.wantErrOut {
				if !strings.Contains(errOut.String(), want) {
					t.Errorf("stderr %q must contain %q", errOut.String(), want)
				}
			}
			if !strings.HasSuffix(errOut.String(), "HTTP: Login successful\n") {
				t.Errorf("stderr = %q, want it to end with the success line", errOut.String())
			}
		})
	}
}

// TestLoginFailureReportsOnce locks the review point that the added status
// output must not double-report: on the failure path login writes no status line
// at all, and the reason plus its upstream label travel in a SINGLE error chain
// that the front end renders as exactly one `Error: …` line.
func TestLoginFailureReportsOnce(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth":
			fmt.Fprint(w, `<html><body><form><input name="login[_token]" value="tok"></form></body></html>`)
		case "/login_check":
			// The callback carries no auth code and the page has no reCAPTCHA
			// marker, so the form login yields nothing and Login fails
			// (website.cpp:339-343).
			http.Redirect(w, r, "/callback", http.StatusFound)
		case "/callback":
			fmt.Fprint(w, "callback")
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	cfg := config.NewConfig(t.TempDir(), t.TempDir())
	cfg.Email, cfg.Password = "user@example.com", "pw"
	var out, errOut bytes.Buffer
	ui := newConsole(strings.NewReader(""), &out, &errOut)
	s := newOfflineSession(t, srv, cfg)

	err := s.login(context.Background(), ui)
	if err == nil {
		t.Fatal("login must fail when the server returns no auth code")
	}
	if !strings.HasPrefix(err.Error(), "Galaxy: Login failed: ") {
		t.Errorf("error = %q, want the upstream label at the head of one error chain", err.Error())
	}
	if got := strings.Count(err.Error(), "Login failed"); got != 1 {
		t.Errorf("error = %q mentions the failure %d times, want exactly one", err.Error(), got)
	}
	if strings.Contains(err.Error(), "Login successful") {
		t.Errorf("error = %q must not carry a success status", err.Error())
	}
	if errOut.Len() != 0 || out.Len() != 0 {
		t.Errorf("failure path wrote stderr %q / stdout %q, want nothing: the error chain is the report",
			errOut.String(), out.String())
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
