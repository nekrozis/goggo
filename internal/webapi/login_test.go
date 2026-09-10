package webapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nekrozis/goggo/internal/config"
	"github.com/nekrozis/goggo/internal/httpx"
)

// newTestClient builds a Client against srv. Default endpoints are spread so
// the www and embed account probes get distinct path prefixes while auth and
// login share the root.
func newTestClient(t *testing.T, srv *httptest.Server, retries int) (*Client, *config.GalaxyConfig) {
	t.Helper()
	galaxy := config.NewGalaxyConfig()
	cl, err := New(httpx.Config{UserAgent: "goggo-test/1.0"}, galaxy, Options{Retries: retries})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cl.ep = endpoints{auth: srv.URL, login: srv.URL, www: srv.URL + "/www", embed: srv.URL + "/embed"}
	return cl, galaxy
}

// loginFormPage returns a realistic login page. The script block contains an
// <input> inside JavaScript to prove the HTML parser does not mis-extract it.
func loginFormPage(withRecaptcha bool) string {
	s := `<html><head><script>var t = "<input name='login[_token]' value='fake-from-js'>";</script></head><body>`
	if withRecaptcha {
		s += `<div class="g-recaptcha form__recaptcha"></div>`
	}
	s += `<form method="post"><input type="hidden" name="login[_token]" value="tok123"></form></body></html>`
	return s
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func tokenJSON() map[string]any {
	return map[string]any{
		"access_token":  "at-1",
		"refresh_token": "rt-1",
		"expires_in":    3600,
		"user_id":       "u7",
	}
}

// successLoginServer models a login that completes without any challenge.
// It returns the server and a *string capturing the auth code that reached
// the token endpoint.
func successLoginServer(t *testing.T, withRecaptcha bool) (*httptest.Server, *string) {
	t.Helper()
	var gotCode string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth":
			fmt.Fprint(w, loginFormPage(withRecaptcha))
		case "/login_check":
			if err := r.ParseForm(); err != nil {
				t.Errorf("login_check ParseForm: %v", err)
			}
			if got := r.Form.Get("login[username]"); got != "user@example.com" {
				t.Errorf("username = %q", got)
			}
			if got := r.Form.Get("login[password]"); got != "secret" {
				t.Errorf("password = %q", got)
			}
			if got := r.Form.Get("login[_token]"); got != "tok123" {
				t.Errorf("_token = %q", got)
			}
			// Relative Location: proves ResolveReference is used, not string
			// concatenation (review lock).
			http.Redirect(w, r, "/cb?code=AUTH1", http.StatusFound)
		case "/cb":
			fmt.Fprint(w, "ok")
		case "/token":
			gotCode = r.URL.Query().Get("code")
			writeJSON(w, tokenJSON())
		default:
			http.NotFound(w, r)
		}
	}))
	return srv, &gotCode
}

func TestLoginSuccess(t *testing.T) {
	srv, gotCode := successLoginServer(t, false)
	defer srv.Close()
	cl, galaxy := newTestClient(t, srv, 2)

	challenge, err := cl.Login(context.Background(), "user@example.com", "secret", LoginOptions{})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if challenge != nil {
		t.Fatalf("Login: unexpected challenge %+v", challenge)
	}
	if *gotCode != "AUTH1" {
		t.Errorf("token endpoint code = %q, want AUTH1", *gotCode)
	}
	if got := galaxy.GetAccessToken(); got != "at-1" {
		t.Errorf("access token = %q", got)
	}
	if got := galaxy.GetRefreshToken(); got != "rt-1" {
		t.Errorf("refresh token = %q", got)
	}
	if galaxy.IsExpired() {
		t.Error("fresh token reported expired")
	}
}

// TestLoginRedirectHop covers a multi-hop 3xx chain before the code URL.
func TestLoginRedirectHop(t *testing.T) {
	var gotCode string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth":
			fmt.Fprint(w, loginFormPage(false))
		case "/login_check":
			http.Redirect(w, r, "/hop1", http.StatusFound)
		case "/hop1":
			http.Redirect(w, r, "/cb?code=HOP", http.StatusFound)
		case "/cb":
			fmt.Fprint(w, "ok")
		case "/token":
			gotCode = r.URL.Query().Get("code")
			writeJSON(w, tokenJSON())
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	cl, _ := newTestClient(t, srv, 2)

	if _, err := cl.Login(context.Background(), "user@example.com", "secret", LoginOptions{}); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if gotCode != "HOP" {
		t.Errorf("token code = %q, want HOP", gotCode)
	}
}

// twoStepServer models a login that redirects to a two-step challenge. It
// exposes the submitted letter names and the auth code that reached the token
// endpoint.
func twoStepServer(t *testing.T, kind codeKind) (*httptest.Server, *string, *string) {
	t.Helper()
	var submitted string
	var gotCode string
	challengePath := "/two_step"
	submitPath := "/login/two_step"
	letterNames := []string{"letter_1", "letter_2", "letter_3", "letter_4"}
	letterPrefix := "second_step_authentication[token]"
	tokenName := "second_step_authentication[_token]"
	if kind == codeTOTP {
		challengePath = "/totp"
		submitPath = "/login/two_factor/totp"
		letterNames = []string{"letter_1", "letter_2", "letter_3", "letter_4", "letter_5", "letter_6"}
		letterPrefix = "two_factor_totp_authentication[token]"
		tokenName = "two_factor_totp_authentication[_token]"
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth":
			fmt.Fprint(w, loginFormPage(false))
		case "/login_check":
			http.Redirect(w, r, challengePath, http.StatusFound)
		case challengePath:
			fmt.Fprintf(w, `<html><body><form><input type="hidden" name="%s" value="ch-tok"></form></body></html>`, tokenName)
		case submitPath:
			if err := r.ParseForm(); err != nil {
				t.Errorf("submit ParseForm: %v", err)
			}
			if got := r.Form.Get(tokenName); got != "ch-tok" {
				t.Errorf("%s = %q", tokenName, got)
			}
			for i, l := range letterNames {
				full := letterPrefix + "[" + l + "]"
				want := fmt.Sprintf("%d", i+1)
				if got := r.Form.Get(full); got != want {
					t.Errorf("letter %d (%s) = %q, want %q", i+1, full, got, want)
				}
			}
			submitted = strings.Join(letterNames, ",")
			http.Redirect(w, r, "/cb?code=TWOFA", http.StatusFound)
		case "/cb":
			fmt.Fprint(w, "ok")
		case "/token":
			gotCode = r.URL.Query().Get("code")
			writeJSON(w, tokenJSON())
		default:
			http.NotFound(w, r)
		}
	}))
	return srv, &submitted, &gotCode
}

func TestLoginTwoStepChallengeAndContinue(t *testing.T) {
	srv, submitted, gotCode := twoStepServer(t, codeTwoStep)
	defer srv.Close()
	cl, _ := newTestClient(t, srv, 2)

	challenge, err := cl.Login(context.Background(), "user@example.com", "secret", LoginOptions{})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if challenge == nil {
		t.Fatal("Login: expected two-factor challenge")
	}
	if challenge.Kind != ChallengeTwoFactor {
		t.Errorf("kind = %v, want two-factor", challenge.Kind)
	}
	if challenge.CodeLength != 4 {
		t.Errorf("CodeLength = %d, want 4", challenge.CodeLength)
	}
	if *submitted != "" {
		t.Fatalf("two-step submitted before ContinueLogin: %q", *submitted)
	}

	// Wrong length: reported as an error, not os.Exit, and nothing is POSTed.
	if err := cl.ContinueLogin(context.Background(), challenge, "12"); err == nil {
		t.Fatal("ContinueLogin: want error for short code")
	} else if !strings.Contains(err.Error(), "must be 4 characters") {
		t.Errorf("error = %v", err)
	}
	if *submitted != "" {
		t.Fatalf("two-step submitted on invalid length: %q", *submitted)
	}

	if err := cl.ContinueLogin(context.Background(), challenge, "1234"); err != nil {
		t.Fatalf("ContinueLogin: %v", err)
	}
	if *submitted != "letter_1,letter_2,letter_3,letter_4" {
		t.Errorf("submitted letters = %q", *submitted)
	}
	if *gotCode != "TWOFA" {
		t.Errorf("token code = %q, want TWOFA", *gotCode)
	}
}

func TestLoginTOTPChallengeAndContinue(t *testing.T) {
	srv, submitted, gotCode := twoStepServer(t, codeTOTP)
	defer srv.Close()
	cl, _ := newTestClient(t, srv, 2)

	challenge, err := cl.Login(context.Background(), "user@example.com", "secret", LoginOptions{})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if challenge == nil || challenge.Kind != ChallengeTwoFactor || challenge.CodeLength != 6 {
		t.Fatalf("unexpected challenge: %+v", challenge)
	}
	if err := cl.ContinueLogin(context.Background(), challenge, "123456"); err != nil {
		t.Fatalf("ContinueLogin: %v", err)
	}
	if *submitted != "letter_1,letter_2,letter_3,letter_4,letter_5,letter_6" {
		t.Errorf("submitted letters = %q", *submitted)
	}
	if *gotCode != "TWOFA" {
		t.Errorf("token code = %q, want TWOFA", *gotCode)
	}
}

// browserLoginServer models a login page that carries reCAPTCHA and whose
// form login yields no code, forcing the browser fallback.
func browserLoginServer(t *testing.T) (*httptest.Server, *string) {
	t.Helper()
	var gotCode string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth":
			fmt.Fprint(w, loginFormPage(true)) // reCAPTCHA present
		case "/login_check":
			http.Redirect(w, r, "/noauth", http.StatusFound)
		case "/noauth":
			fmt.Fprint(w, "login blocked, no code here")
		case "/paste":
			fmt.Fprint(w, "callback consumed")
		case "/token":
			gotCode = r.URL.Query().Get("code")
			writeJSON(w, tokenJSON())
		default:
			http.NotFound(w, r)
		}
	}))
	return srv, &gotCode
}

func TestLoginRecaptchaFallsBackToBrowser(t *testing.T) {
	srv, gotCode := browserLoginServer(t)
	defer srv.Close()
	cl, _ := newTestClient(t, srv, 2)

	challenge, err := cl.Login(context.Background(), "user@example.com", "secret", LoginOptions{})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if challenge == nil || challenge.Kind != ChallengeBrowser {
		t.Fatalf("expected browser challenge, got %+v", challenge)
	}
	if !strings.Contains(challenge.BrowserURL, "/auth?") {
		t.Errorf("BrowserURL = %q, want authorize URL", challenge.BrowserURL)
	}

	callback := srv.URL + "/paste?code=BR1"
	if err := cl.ContinueLogin(context.Background(), challenge, callback); err != nil {
		t.Fatalf("ContinueLogin: %v", err)
	}
	if *gotCode != "BR1" {
		t.Errorf("token code = %q, want BR1", *gotCode)
	}
}

func TestLoginForceBrowser(t *testing.T) {
	srv, _ := successLoginServer(t, false)
	defer srv.Close()
	cl, _ := newTestClient(t, srv, 2)

	challenge, err := cl.Login(context.Background(), "user@example.com", "secret",
		LoginOptions{ForceBrowser: true})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if challenge == nil || challenge.Kind != ChallengeBrowser {
		t.Fatalf("expected browser challenge, got %+v", challenge)
	}
}

func TestLoginMissingToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/auth" {
			fmt.Fprint(w, `<html><body><form><input name="other" value="1"></form></body></html>`)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	cl, _ := newTestClient(t, srv, 2)

	challenge, err := cl.Login(context.Background(), "user@example.com", "secret", LoginOptions{})
	if err == nil {
		t.Fatal("Login: want error for missing login token")
	}
	if !strings.Contains(err.Error(), "login token") {
		t.Errorf("error = %v", err)
	}
	if challenge != nil {
		t.Errorf("expected no challenge on error, got %v", challenge)
	}
}

func TestTokenExchangeRetries(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth":
			fmt.Fprint(w, loginFormPage(false))
		case "/login_check":
			http.Redirect(w, r, "/cb?code=AUTH1", http.StatusFound)
		case "/cb":
			fmt.Fprint(w, "ok")
		case "/token":
			hits++
			if hits < 3 {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			writeJSON(w, tokenJSON())
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	cl, _ := newTestClient(t, srv, 2) // min(3,2)+1 = 3 attempts

	if _, err := cl.Login(context.Background(), "user@example.com", "secret", LoginOptions{}); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if hits != 3 {
		t.Errorf("token hits = %d, want 3", hits)
	}
}

func TestExtractCodeTable(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"http://h/cb?code=abc", "abc"},
		{"http://h/cb?code=abc&foo=1", "abc"},
		{"http://h/cb?a=1&code=abc", "abc"},
		{"http://h/cb?CODE=AbC", "AbC"}, // icase
		{"http://h/cb?code=", ""},
		{"code=abc", "abc"},
		{"http://h/cb?nobody=1", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := extractCode(c.in); got != c.want {
			t.Errorf("extractCode(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestTokenRetryBoundaries locks the retry-count semantics at the edges:
// Retries=0 yields exactly one attempt (no retry), and Retries larger than 3
// caps at min(3,Retries)+1 attempts (website.cpp:33 clamps to 3).
func TestTokenRetryBoundaries(t *testing.T) {
	for _, tc := range []struct {
		retries  int
		wantHits int
		wantErr  bool
	}{
		{retries: 0, wantHits: 1, wantErr: true},  // single attempt, 500 -> fail
		{retries: 10, wantHits: 4, wantErr: true}, // capped at min(3,10)+1 = 4
	} {
		hits := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/auth":
				fmt.Fprint(w, loginFormPage(false))
			case "/login_check":
				http.Redirect(w, r, "/cb?code=AUTH1", http.StatusFound)
			case "/cb":
				fmt.Fprint(w, "ok")
			case "/token":
				hits++
				w.WriteHeader(http.StatusInternalServerError)
			default:
				http.NotFound(w, r)
			}
		}))
		cl, _ := newTestClient(t, srv, tc.retries)

		_, err := cl.Login(context.Background(), "user@example.com", "secret", LoginOptions{})
		if (err != nil) != tc.wantErr {
			t.Errorf("retries=%d: err = %v, wantErr %v", tc.retries, err, tc.wantErr)
		}
		if hits != tc.wantHits {
			t.Errorf("retries=%d: token hits = %d, want %d", tc.retries, hits, tc.wantHits)
		}
		srv.Close()
	}
}

func TestIsLoggedInDirect200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/www/account" {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	cl, _ := newTestClient(t, srv, 0)

	ok, err := cl.IsLoggedIn(context.Background())
	if err != nil {
		t.Fatalf("IsLoggedIn: %v", err)
	}
	if !ok {
		t.Error("IsLoggedIn = false, want true for direct 200")
	}
}

// TestIsLoggedInEmbedRedirect models the logged-in-elsewhere redirect:
// www.gog.com/account 302 -> embed.gog.com/account (200) => logged in. The
// relative Location exercises ResolveReference against the request URL.
func TestIsLoggedInEmbedRedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/www/account":
			http.Redirect(w, r, "/embed/account", http.StatusFound)
		case "/embed/account":
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	cl, _ := newTestClient(t, srv, 0)

	ok, err := cl.IsLoggedIn(context.Background())
	if err != nil {
		t.Fatalf("IsLoggedIn: %v", err)
	}
	if !ok {
		t.Error("IsLoggedIn = false, want true after embed account redirect")
	}
}

func TestIsLoggedInRedirectElsewhereIsFalse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/www/account" {
			http.Redirect(w, r, "/login-page", http.StatusFound)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	cl, _ := newTestClient(t, srv, 0)

	ok, err := cl.IsLoggedIn(context.Background())
	if err != nil {
		t.Fatalf("IsLoggedIn: %v", err)
	}
	if ok {
		t.Error("IsLoggedIn = true, want false for non-embed redirect")
	}
}

func TestLoginNilGalaxyRejected(t *testing.T) {
	if _, err := New(httpx.Config{}, nil, Options{}); err == nil {
		t.Fatal("New: want error for nil galaxy")
	}
}

// TestContinueLoginConsumedOnSecondCall locks the one-shot contract: after a
// successful continuation the same challenge cannot be used again.
func TestContinueLoginConsumedOnSecondCall(t *testing.T) {
	srv, _, _ := twoStepServer(t, codeTwoStep)
	defer srv.Close()
	cl, _ := newTestClient(t, srv, 2)

	challenge, err := cl.Login(context.Background(), "user@example.com", "secret", LoginOptions{})
	if err != nil || challenge == nil {
		t.Fatalf("Login: %v / %+v", err, challenge)
	}
	if err := cl.ContinueLogin(context.Background(), challenge, "1234"); err != nil {
		t.Fatalf("ContinueLogin: %v", err)
	}
	if err := cl.ContinueLogin(context.Background(), challenge, "1234"); !errors.Is(err, ErrChallengeConsumed) {
		t.Errorf("second ContinueLogin err = %v, want ErrChallengeConsumed", err)
	}
}

// TestContinueLoginConsumedBeforeSideEffect locks the v4 review point: the
// challenge is consumed BEFORE the network submission, so a failed submission
// still cannot be retried with the same challenge.
func TestContinueLoginConsumedBeforeSideEffect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth":
			fmt.Fprint(w, loginFormPage(false))
		case "/login_check":
			http.Redirect(w, r, "/two_step", http.StatusFound)
		case "/two_step":
			fmt.Fprint(w, `<html><body><form><input type="hidden" name="second_step_authentication[_token]" value="ch-tok"></form></body></html>`)
		case "/login/two_step":
			// Submission always fails at the protocol level.
			w.WriteHeader(http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	cl, _ := newTestClient(t, srv, 2)

	challenge, err := cl.Login(context.Background(), "user@example.com", "secret", LoginOptions{})
	if err != nil || challenge == nil {
		t.Fatalf("Login: %v / %+v", err, challenge)
	}
	if err := cl.ContinueLogin(context.Background(), challenge, "1234"); err == nil {
		t.Fatal("ContinueLogin: want error for failed submission")
	}
	// Consumed before the side effect: the retry must be refused.
	if err := cl.ContinueLogin(context.Background(), challenge, "1234"); !errors.Is(err, ErrChallengeConsumed) {
		t.Errorf("retry after failure err = %v, want ErrChallengeConsumed", err)
	}
}

// TestContinueLoginRejectsForeignClient locks the Client binding: a challenge
// may only be continued by the Client that created it, and a mismatch does
// not consume the challenge (the owning client can still finish it).
func TestContinueLoginRejectsForeignClient(t *testing.T) {
	srv, _, _ := twoStepServer(t, codeTwoStep)
	defer srv.Close()
	owner, _ := newTestClient(t, srv, 2)
	foreign, _ := newTestClient(t, srv, 2)

	challenge, err := owner.Login(context.Background(), "user@example.com", "secret", LoginOptions{})
	if err != nil || challenge == nil {
		t.Fatalf("Login: %v / %+v", err, challenge)
	}
	if err := foreign.ContinueLogin(context.Background(), challenge, "1234"); !errors.Is(err, ErrChallengeClientMismatch) {
		t.Errorf("foreign ContinueLogin err = %v, want ErrChallengeClientMismatch", err)
	}
	// Not consumed: the owning client can still complete the login.
	if err := owner.ContinueLogin(context.Background(), challenge, "1234"); err != nil {
		t.Errorf("owner ContinueLogin after mismatch: %v", err)
	}
}

// TestNewPreservesCallerShouldRetry locks the R09a-2 fix: New may override
// only MaxAttempts and Wait of the httpx policy, never the caller's ShouldRetry
// predicate. A predicate that refuses every retry must yield exactly one hit.
func TestNewPreservesCallerShouldRetry(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := httpx.Config{
		UserAgent: "goggo-test/1.0",
		RetryPolicy: httpx.RetryPolicy{
			ShouldRetry: func(*http.Response, error) bool { return false },
		},
	}
	cl, err := New(cfg, config.NewGalaxyConfig(), Options{Retries: 3})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cl.ep = endpoints{auth: srv.URL, login: srv.URL, www: srv.URL, embed: srv.URL}

	if _, err := cl.Login(context.Background(), "user@example.com", "secret", LoginOptions{}); err == nil {
		t.Fatal("Login: want error for HTTP 500")
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("hits = %d, want 1 (caller ShouldRetry must not be overwritten)", got)
	}
}

// TestLoginRetryWaitApplied verifies Options.Wait reaches the retry policy:
// with Retries=2 there are two extra attempts and therefore two waits. Only a
// lower bound is asserted so scheduling jitter (which can only add time)
// cannot make the test flaky; it checks that the waits happened, not how long
// the request took.
func TestLoginRetryWaitApplied(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	cl, err := New(httpx.Config{UserAgent: "goggo-test/1.0"}, config.NewGalaxyConfig(),
		Options{Retries: 2, Wait: 50 * time.Millisecond})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cl.ep = endpoints{auth: srv.URL, login: srv.URL, www: srv.URL, embed: srv.URL}

	start := time.Now()
	if _, err := cl.Login(context.Background(), "user@example.com", "secret", LoginOptions{}); err == nil {
		t.Fatal("Login: want error for HTTP 500")
	}
	if elapsed := time.Since(start); elapsed < 100*time.Millisecond {
		t.Errorf("elapsed = %v, want >= 100ms (two 50ms retry waits)", elapsed)
	}
}

// TestWalkRedirectChainBoundaries pins the terminal cases of the manual
// redirect walk: a 3xx without a Location, a non-3xx response without a code,
// and a self-referential redirect must each end the walk instead of looping.
func TestWalkRedirectChainBoundaries(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/noloc", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusFound) // 302 with no Location header
	})
	mux.HandleFunc("/plain", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "no code here")
	})
	mux.HandleFunc("/loop", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/loop", http.StatusFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cl, _ := newTestClient(t, srv, 0)

	for _, tc := range []struct {
		name string
		in   string
	}{
		{"3xx without location", srv.URL + "/noloc"},
		{"non-3xx without code", srv.URL + "/plain"},
		{"self redirect", srv.URL + "/loop"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, consumeURL, err := cl.walkRedirectChain(context.Background(), tc.in)
			if err != nil {
				t.Fatalf("walkRedirectChain: %v", err)
			}
			if code != "" {
				t.Errorf("code = %q, want empty", code)
			}
			if consumeURL == "" {
				t.Errorf("consumeURL = %q, want the current url", consumeURL)
			}
		})
	}
}
