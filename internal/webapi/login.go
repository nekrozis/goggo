package webapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// CodeKind distinguishes the two one-time-code flavours internally.
type CodeKind int

const (
	// CodeTwoStep is the GOG second-step e-mail code (4 characters).
	CodeTwoStep CodeKind = iota
	// CodeTOTP is the authenticator TOTP code (6 characters).
	CodeTOTP
)

func (k CodeKind) String() string {
	switch k {
	case CodeTwoStep:
		return "two-step"
	case CodeTOTP:
		return "totp"
	default:
		return "code-kind(" + strconv.Itoa(int(k)) + ")"
	}
}

// ChallengeKind tells the caller which interaction Login needs.
type ChallengeKind uint8

const (
	// ChallengeTwoFactor asks for a one-time security code (length in
	// LoginChallenge.CodeLength). ContinueLogin expects the code.
	ChallengeTwoFactor ChallengeKind = iota + 1
	// ChallengeBrowser asks the user to finish login in a browser opened at
	// LoginChallenge.BrowserURL. ContinueLogin expects the pasted callback
	// URL.
	ChallengeBrowser
)

func (k ChallengeKind) String() string {
	switch k {
	case ChallengeTwoFactor:
		return "two-factor"
	case ChallengeBrowser:
		return "browser"
	default:
		return "challenge(" + strconv.Itoa(int(k)) + ")"
	}
}

// LoginOptions are the per-call policy knobs for Login.
type LoginOptions struct {
	// ForceBrowser mirrors bForceBrowserLogin (--browser-login): it skips
	// the curl-based form login and goes straight to the browser challenge.
	// This is a CLI/config policy, so it lives here rather than inside the
	// Client.
	ForceBrowser bool
}

// LoginResult is a successful-login marker. The acquired tokens are stored
// in the GalaxyConfig passed at construction (SetJSON), not duplicated here.
type LoginResult struct{}

// LoginChallenge describes an interaction the login flow needs. It carries
// opaque state (CSRF token, submit endpoint, code kind) that the caller must
// NOT interpret; the caller only reads Kind/CodeLength/BrowserURL to prompt
// the user, then passes the obtained string to ContinueLogin.
//
// Lifecycle contract: a challenge is bound to the Client that produced it
// (its state belongs to that client's session and cookie jar). It is not
// serialisable, must not be handed to a different Client, and must not be
// used concurrently. ContinueLogin consumes it once.
type LoginChallenge struct {
	Kind ChallengeKind
	// CodeLength is the expected one-time code length for a
	// ChallengeTwoFactor challenge (4 or 6, website.cpp:515-525).
	CodeLength int
	// BrowserURL is the authorize URL to open for a ChallengeBrowser
	// challenge.
	BrowserURL string

	// state is the opaque continuation state owned by Client.
	state challengeState
}

// challengeState holds what ContinueLogin needs to finish a two-factor
// submission: the challenge CSRF token and the submit endpoint, plus which
// code flavour it is.
type challengeState struct {
	kind   CodeKind
	token  string
	submit string
}

// recaptchaMarker is the login-form fragment that indicates a captcha
// (website.cpp:365).
const recaptchaMarker = `class="g-recaptcha form__recaptcha"`

// Form token input names and code lengths (website.cpp:388-393,475-493).
const (
	loginFormToken = "login[_token]"
	twoStepToken   = "second_step_authentication[_token]"
	totpToken      = "two_factor_totp_authentication[_token]"

	twoStepCodeLength = 4
	totpCodeLength    = 6
)

// Login performs the website OAuth login (website.cpp:306-380) up to the
// first interaction it needs. It resets the client credentials, fetches the
// login form, tries the curl-based form login unless ForceBrowser is set, and
// then either completes the token exchange (returning a non-nil LoginResult)
// or returns a LoginChallenge:
//
//   - ChallengeTwoFactor: two-step e-mail or TOTP verification is required.
//   - ChallengeBrowser: reCAPTCHA blocked the form login, or ForceBrowser
//     was set; the user must finish login in a browser.
//
// Complete the flow with ContinueLogin once the user supplied the code or
// callback URL. Cookie persistence (the C++ COOKIELIST FLUSH) is deferred to
// S10; the session cookie jar already lives inside the httpx Client.
func (c *Client) Login(ctx context.Context, email, password string, opts LoginOptions) (*LoginResult, *LoginChallenge, error) {
	c.galaxy.ResetClient()

	authURL := c.authURL()
	formHTML, err := c.getResponse(ctx, authURL)
	if err != nil {
		return nil, nil, fmt.Errorf("webapi: fetch login form: %w", err)
	}
	bRecaptcha := strings.Contains(formHTML, recaptchaMarker)

	if !opts.ForceBrowser {
		code, challenge, formErr := c.formLogin(ctx, formHTML, email, password)
		if challenge != nil {
			return nil, challenge, nil
		}
		if code != "" {
			if err := c.exchangeCode(ctx, code); err != nil {
				return nil, nil, err
			}
			return &LoginResult{}, nil, nil
		}
		// No code: either the form failed or the server wants a captcha.
		// The C++ source falls back to the browser only when reCAPTCHA was
		// detected (website.cpp:376).
		if !bRecaptcha {
			if formErr != nil {
				return nil, nil, formErr
			}
			return nil, nil, errors.New("webapi: failed to get auth code")
		}
	}

	return nil, &LoginChallenge{Kind: ChallengeBrowser, BrowserURL: authURL}, nil
}

// ContinueLogin finishes a Login that returned a LoginChallenge. response is
// the security code for ChallengeTwoFactor or the pasted callback URL for
// ChallengeBrowser. The opaque state inside challenge drives the flow.
func (c *Client) ContinueLogin(ctx context.Context, challenge *LoginChallenge, response string) (*LoginResult, error) {
	if challenge == nil {
		return nil, errors.New("webapi: nil login challenge")
	}
	switch challenge.Kind {
	case ChallengeTwoFactor:
		if len(response) != challenge.CodeLength {
			// The C++ source exits here (website.cpp:528-532); Go reports
			// the invalid length instead of terminating the process.
			return nil, fmt.Errorf("webapi: security code must be %d characters long", challenge.CodeLength)
		}
		code, err := c.submitSecurityCode(ctx, challenge.state, response)
		if err != nil {
			return nil, err
		}
		return c.finishWithCode(ctx, code)
	case ChallengeBrowser:
		code := extractCode(response)
		if code == "" {
			return nil, errors.New("webapi: no auth code in the url pasted from the browser")
		}
		// Consume the callback URL once with redirects enabled
		// (website.cpp:627-635). Errors there are printed but do not discard
		// the code in the C++ source, so they are ignored here too.
		_ = c.followGet(ctx, response)
		return c.finishWithCode(ctx, code)
	default:
		return nil, fmt.Errorf("webapi: unknown challenge kind %d", challenge.Kind)
	}
}

// formLogin runs the non-interactive part of the curl-based login
// (website.cpp:382-607). It returns an auth code when the flow completed
// without a challenge, or a LoginChallenge when two-step/TOTP verification is
// required (the challenge page token is fetched here so ContinueLogin only
// submits).
func (c *Client) formLogin(ctx context.Context, formHTML, email, password string) (string, *LoginChallenge, error) {
	token, err := extractInputValue([]byte(formHTML), loginFormToken)
	if err != nil {
		return "", nil, err
	}
	if token == "" {
		return "", nil, errors.New("webapi: failed to get login token")
	}

	// POST order is irrelevant to the server (fields are matched by name);
	// url.Values.Encode sorts keys, which only changes the byte order the
	// C++ source produced with its hand-built string.
	post := url.Values{}
	post.Set("login[username]", email)
	post.Set("login[password]", password)
	post.Set("login[login]", "")
	post.Set("login[_token]", token)

	resp, err := c.postForm(ctx, c.ep.login+"/login_check", post.Encode())
	if err != nil {
		return "", nil, fmt.Errorf("webapi: login_check: %w", err)
	}
	redirectURL := c.resolveLocation(resp)

	// Two step authorization (website.cpp:446-569): fetch the challenge page
	// and surface a ChallengeTwoFactor carrying its token.
	challenge, err := c.maybeChallenge(ctx, redirectURL)
	if err != nil {
		return "", nil, err
	}
	if challenge != nil {
		return "", challenge, nil
	}

	code, finalURL, err := c.walkRedirectChain(ctx, redirectURL)
	if err != nil {
		return "", nil, err
	}
	if finalURL != "" {
		_ = c.followGet(ctx, finalURL)
	}
	return code, nil, nil
}

// maybeChallenge detects a two-step or TOTP redirect and prepares a
// ChallengeTwoFactor by fetching the challenge page and extracting its CSRF
// token. It returns nil when the redirect does not indicate a challenge.
func (c *Client) maybeChallenge(ctx context.Context, redirectURL string) (*LoginChallenge, error) {
	var kind CodeKind
	var tokenName string
	var length int
	var submit string
	switch {
	case strings.Contains(redirectURL, "two_step"):
		kind, tokenName, length = CodeTwoStep, twoStepToken, twoStepCodeLength
		submit = c.ep.login + "/login/two_step"
	case strings.Contains(redirectURL, "totp"):
		kind, tokenName, length = CodeTOTP, totpToken, totpCodeLength
		submit = c.ep.login + "/login/two_factor/totp"
	default:
		return nil, nil
	}
	page, err := c.getResponse(ctx, redirectURL)
	if err != nil {
		return nil, fmt.Errorf("webapi: fetch %s page: %w", kind, err)
	}
	token, err := extractInputValue([]byte(page), tokenName)
	if err != nil {
		return nil, err
	}
	if token == "" {
		return nil, fmt.Errorf("webapi: no %s token in challenge page", kind)
	}
	return &LoginChallenge{
		Kind:       ChallengeTwoFactor,
		CodeLength: length,
		state:      challengeState{kind: kind, token: token, submit: submit},
	}, nil
}

// submitSecurityCode POSTs the one-time code for the challenge held in state
// and returns the auth code extracted from the resulting redirect chain
// (website.cpp:534-569,571-593).
func (c *Client) submitSecurityCode(ctx context.Context, state challengeState, code string) (string, error) {
	post := url.Values{}
	switch state.kind {
	case CodeTwoStep:
		post.Set("second_step_authentication[send]", "")
		for i, l := range []string{"letter_1", "letter_2", "letter_3", "letter_4"} {
			post.Set("second_step_authentication[token]["+l+"]", string(code[i]))
		}
	case CodeTOTP:
		post.Set("two_factor_totp_authentication[send]", "")
		for i, l := range []string{"letter_1", "letter_2", "letter_3", "letter_4", "letter_5", "letter_6"} {
			post.Set("two_factor_totp_authentication[token]["+l+"]", string(code[i]))
		}
	}
	tokenName := twoStepToken
	if state.kind == CodeTOTP {
		tokenName = totpToken
	}
	post.Set(tokenName, state.token)

	resp, err := c.postForm(ctx, state.submit, post.Encode())
	if err != nil {
		return "", fmt.Errorf("webapi: submit %s code: %w", state.kind, err)
	}
	redirectURL := c.resolveLocation(resp)

	authCode, finalURL, err := c.walkRedirectChain(ctx, redirectURL)
	if err != nil {
		return "", err
	}
	if finalURL != "" {
		_ = c.followGet(ctx, finalURL)
	}
	if authCode == "" {
		return "", errors.New("webapi: failed to get auth code")
	}
	return authCode, nil
}

// finishWithCode exchanges an authorization code at the token endpoint and
// stores the result in galaxy (website.cpp:316-337).
func (c *Client) finishWithCode(ctx context.Context, code string) (*LoginResult, error) {
	if err := c.exchangeCode(ctx, code); err != nil {
		return nil, err
	}
	return &LoginResult{}, nil
}

// exchangeCode performs the token exchange (website.cpp:318-337).
func (c *Client) exchangeCode(ctx context.Context, code string) error {
	q := url.Values{}
	q.Set("client_id", c.galaxy.GetClientID())
	q.Set("client_secret", c.galaxy.GetClientSecret())
	q.Set("grant_type", "authorization_code")
	q.Set("code", code)
	q.Set("redirect_uri", c.galaxy.GetRedirectURI())
	tokenBody, err := c.getResponse(ctx, c.ep.auth+"/token?"+q.Encode())
	if err != nil {
		return fmt.Errorf("webapi: token exchange: %w", err)
	}
	token, err := decodeJSONObject(tokenBody)
	if err != nil {
		return fmt.Errorf("webapi: parse token response: %w", err)
	}
	c.galaxy.SetJSON(token)
	return nil
}

// walkRedirectChain follows 3xx redirects manually and extracts the auth code
// from the callback URL (website.cpp:571-593). Each step performs a GET
// without auto-redirect. When the response is 3xx the chain continues with
// the new Location; the code is checked on the current URL after each step.
// The final URL is returned for the trailing consume-GET the C++ source
// performs.
func (c *Client) walkRedirectChain(ctx context.Context, redirectURL string) (code, finalURL string, err error) {
	cur := redirectURL
	for cur != "" {
		resp, err := c.noRedirectGet(ctx, cur)
		if err != nil {
			return "", "", err
		}
		is3xx := resp.StatusCode/100 == 3
		if is3xx {
			next := c.resolveLocation(resp)
			if next == "" || next == cur {
				return "", cur, nil
			}
			cur = next
		}
		if code := extractCode(cur); code != "" {
			return code, cur, nil
		}
		if !is3xx {
			return "", cur, nil
		}
	}
	return "", "", nil
}

// postForm sends an application/x-www-form-urlencoded POST without following
// redirects and drains the response body.
func (c *Client) postForm(ctx context.Context, url, body string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.hx.DoNoRedirect(ctx, req)
	if err != nil {
		return nil, err
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp, nil
}
