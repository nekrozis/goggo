package webapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/nekrozis/goggo/internal/httpx"
)

// codeKind distinguishes the two one-time-code flavours. It is an internal
// protocol detail: the public API only exposes ChallengeKind, and error
// messages use codeLabel rather than depending on this type's formatting.
type codeKind uint8

const (
	// codeTwoStep is the GOG second-step e-mail code (4 characters).
	codeTwoStep codeKind = iota
	// codeTOTP is the authenticator TOTP code (6 characters).
	codeTOTP
)

// codeLabel names a code flavour for error messages and diagnostics.
func codeLabel(k codeKind) string {
	if k == codeTOTP {
		return "totp"
	}
	return "two-step"
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
	// ForceBrowser skips the form login and goes straight to the browser
	// challenge (the CLI's --browser-login). This is a CLI/config policy, so
	// it lives here rather than inside the Client.
	ForceBrowser bool
}

// Sentinel errors for the LoginChallenge lifecycle. Other failures are
// returned as wrapped errors; these two are part of the API contract.
var (
	// ErrChallengeConsumed is returned when a LoginChallenge is passed to
	// ContinueLogin a second time.
	ErrChallengeConsumed = errors.New("webapi: login challenge already consumed")
	// ErrChallengeClientMismatch is returned when a LoginChallenge is handed
	// to a Client other than the one that created it.
	ErrChallengeClientMismatch = errors.New("webapi: login challenge belongs to a different client")
)

// LoginChallenge describes an interaction the login flow needs. It carries
// opaque state (CSRF token, submit endpoint, code kind) that the caller must
// NOT interpret; the caller only reads Kind/CodeLength/BrowserURL to prompt
// the user, then passes the obtained string to ContinueLogin.
//
// Lifecycle (enforced at runtime, not just documented): a challenge is bound
// to the Client that produced it (its state belongs to that client's session
// and cookie jar), and ContinueLogin consumes it exactly once — a second
// call returns ErrChallengeConsumed. It is not serialisable and must not be
// used concurrently.
//
// It deliberately has no String/fmt.Stringer method: the state holds an
// authentication CSRF token that must never leak into logs.
type LoginChallenge struct {
	// state is the opaque continuation state owned by Client.
	state challengeState

	// BrowserURL is the authorize URL to open for a ChallengeBrowser
	// challenge.
	BrowserURL string

	// CodeLength is the expected one-time code length for a
	// ChallengeTwoFactor challenge (4 or 6).
	CodeLength int

	// client is the Client that produced this challenge.
	client *Client

	// used is set atomically before the first network side effect of
	// ContinueLogin, so a challenge can never be submitted twice.
	used atomic.Bool

	Kind ChallengeKind
}

// challengeState holds what ContinueLogin needs to finish a two-factor
// submission: the challenge CSRF token and the submit endpoint, plus which
// code flavour it is.
type challengeState struct {
	kind   codeKind
	token  string
	submit string
}

// recaptchaMarker is the login-form fragment that indicates a captcha.
const recaptchaMarker = `class="g-recaptcha form__recaptcha"`

// Form token input names and code lengths.
const (
	loginFormToken = "login[_token]"
	twoStepToken   = "second_step_authentication[_token]"
	totpToken      = "two_factor_totp_authentication[_token]"

	twoStepCodeLength = 4
	totpCodeLength    = 6
)

// loginStep is the outcome of the non-interactive login attempt: either an
// auth code that can be exchanged, or a challenge the caller must answer.
// Invariant: code != "" implies challenge == nil, and challenge != nil
// implies code == "".
type loginStep struct {
	code      string
	challenge *LoginChallenge
}

// Login performs the website OAuth login up to the first interaction it needs.
// It resets the client credentials, fetches the login form, tries the form login
// unless ForceBrowser is set, and then either completes the token exchange or
// returns a LoginChallenge:
//
//   - nil challenge, nil error: login completed; tokens are in the
//     GalaxyConfig passed at construction.
//   - challenge, nil error: user interaction is required; call ContinueLogin.
//   - nil challenge, error: login failed.
//
// The session cookie jar lives inside the httpx Client; persisting it is the
// caller's job (see httpx.Client.SaveCookies).
func (c *Client) Login(ctx context.Context, email, password string, opts LoginOptions) (*LoginChallenge, error) {
	c.galaxy.ResetClient()

	authURL := c.authURL()
	formHTML, err := c.getResponse(ctx, authURL)
	if err != nil {
		return nil, fmt.Errorf("webapi: fetch login form: %w", err)
	}
	bRecaptcha := strings.Contains(formHTML, recaptchaMarker)

	if !opts.ForceBrowser {
		step, formErr := c.formLogin(ctx, formHTML, email, password)
		if step.challenge != nil {
			return step.challenge, nil
		}
		if step.code != "" {
			if err := c.exchangeCode(ctx, step.code); err != nil {
				return nil, err
			}
			return nil, nil
		}
		// No code: either the form failed or the server wants a captcha.
		// Fall back to the browser only when reCAPTCHA was detected.
		if !bRecaptcha {
			if formErr != nil {
				return nil, formErr
			}
			return nil, errors.New("webapi: failed to get auth code")
		}
	}

	return c.newBrowserChallenge(authURL), nil
}

// ContinueLogin finishes a Login that returned a LoginChallenge. response is
// the security code for ChallengeTwoFactor or the pasted callback URL for
// ChallengeBrowser. The opaque state inside challenge drives the flow.
//
// The challenge must belong to c and may be used only once. An invalid
// response (wrong code length, callback URL without a code) is a caller error
// that does NOT consume the challenge, so the user can retry. Once a valid
// response starts the network submission, the challenge is consumed even if
// the request fails — the same challenge can never be submitted twice.
func (c *Client) ContinueLogin(ctx context.Context, challenge *LoginChallenge, response string) error {
	if challenge == nil {
		return errors.New("webapi: nil login challenge")
	}
	if challenge.client != c {
		return ErrChallengeClientMismatch
	}

	// Validate first: invalid arguments must leave the challenge reusable.
	code := ""
	switch challenge.Kind {
	case ChallengeTwoFactor:
		if len(response) != challenge.CodeLength {
			return fmt.Errorf("webapi: security code must be %d characters long", challenge.CodeLength)
		}
	case ChallengeBrowser:
		code = extractCode(response)
		if code == "" {
			return errors.New("webapi: no auth code in the url pasted from the browser")
		}
	default:
		return fmt.Errorf("webapi: unknown challenge kind %d", challenge.Kind)
	}

	// Consume once, atomically, BEFORE any network side effect: a failed
	// submission must not be retriable with the same challenge.
	if !challenge.used.CompareAndSwap(false, true) {
		return ErrChallengeConsumed
	}

	switch challenge.Kind {
	case ChallengeTwoFactor:
		authCode, err := c.submitSecurityCode(ctx, challenge.state, response)
		if err != nil {
			return err
		}
		return c.finishWithCode(ctx, authCode)
	case ChallengeBrowser:
		// Consume the callback URL once with redirects enabled. A failure there
		// is ignored: the code has already been extracted.
		_ = c.followGet(ctx, response)
		return c.finishWithCode(ctx, code)
	}
	return nil // unreachable: kind validated above
}

// newBrowserChallenge builds a browser challenge bound to c.
func (c *Client) newBrowserChallenge(authURL string) *LoginChallenge {
	return &LoginChallenge{
		client:     c,
		BrowserURL: authURL,
		Kind:       ChallengeBrowser,
	}
}

// formLogin runs the non-interactive part of the login flow. It returns a
// loginStep carrying either an auth code
// (flow completed without a challenge) or a LoginChallenge when two-step/TOTP
// verification is required (the challenge page token is fetched here so
// ContinueLogin only submits).
func (c *Client) formLogin(ctx context.Context, formHTML, email, password string) (loginStep, error) {
	token, err := extractInputValue([]byte(formHTML), loginFormToken)
	if err != nil {
		return loginStep{}, err
	}
	if token == "" {
		return loginStep{}, errors.New("webapi: failed to get login token")
	}

	// POST order is irrelevant to the server (fields are matched by name);
	// url.Values.Encode sorts the keys.
	post := url.Values{}
	post.Set("login[username]", email)
	post.Set("login[password]", password)
	post.Set("login[login]", "")
	post.Set("login[_token]", token)

	meta, err := c.postForm(ctx, c.ep.login+"/login_check", post.Encode())
	if err != nil {
		return loginStep{}, fmt.Errorf("webapi: login_check: %w", err)
	}
	redirectURL := c.resolveLocation(meta.URL, meta.Location)

	// Two step authorization: fetch the challenge page
	// and surface a ChallengeTwoFactor carrying its token.
	challenge, err := c.maybeChallenge(ctx, redirectURL)
	if err != nil {
		return loginStep{}, err
	}
	if challenge != nil {
		return loginStep{challenge: challenge}, nil
	}

	code, consumeURL, err := c.walkRedirectChain(ctx, redirectURL)
	if err != nil {
		return loginStep{}, err
	}
	if consumeURL != "" {
		_ = c.followGet(ctx, consumeURL)
	}
	return loginStep{code: code}, nil
}

// maybeChallenge detects a two-step or TOTP redirect and prepares a
// ChallengeTwoFactor by fetching the challenge page and extracting its CSRF
// token. It returns nil when the redirect does not indicate a challenge.
func (c *Client) maybeChallenge(ctx context.Context, redirectURL string) (*LoginChallenge, error) {
	var kind codeKind
	var tokenName string
	var length int
	var submit string
	switch {
	case strings.Contains(redirectURL, "two_step"):
		kind, tokenName, length = codeTwoStep, twoStepToken, twoStepCodeLength
		submit = c.ep.login + "/login/two_step"
	case strings.Contains(redirectURL, "totp"):
		kind, tokenName, length = codeTOTP, totpToken, totpCodeLength
		submit = c.ep.login + "/login/two_factor/totp"
	default:
		return nil, nil
	}
	page, err := c.getResponse(ctx, redirectURL)
	if err != nil {
		return nil, fmt.Errorf("webapi: fetch %s page: %w", codeLabel(kind), err)
	}
	token, err := extractInputValue([]byte(page), tokenName)
	if err != nil {
		return nil, err
	}
	if token == "" {
		return nil, fmt.Errorf("webapi: no %s token in challenge page", codeLabel(kind))
	}
	return &LoginChallenge{
		client:     c,
		CodeLength: length,
		Kind:       ChallengeTwoFactor,
		state:      challengeState{kind: kind, token: token, submit: submit},
	}, nil
}

// submitSecurityCode POSTs the one-time code for the challenge held in state
// and returns the auth code extracted from the resulting redirect chain.
func (c *Client) submitSecurityCode(ctx context.Context, state challengeState, code string) (string, error) {
	post := url.Values{}
	switch state.kind {
	case codeTwoStep:
		post.Set("second_step_authentication[send]", "")
		for i, l := range []string{"letter_1", "letter_2", "letter_3", "letter_4"} {
			post.Set("second_step_authentication[token]["+l+"]", string(code[i]))
		}
	case codeTOTP:
		post.Set("two_factor_totp_authentication[send]", "")
		for i, l := range []string{"letter_1", "letter_2", "letter_3", "letter_4", "letter_5", "letter_6"} {
			post.Set("two_factor_totp_authentication[token]["+l+"]", string(code[i]))
		}
	}
	tokenName := twoStepToken
	if state.kind == codeTOTP {
		tokenName = totpToken
	}
	post.Set(tokenName, state.token)

	meta, err := c.postForm(ctx, state.submit, post.Encode())
	if err != nil {
		return "", fmt.Errorf("webapi: submit %s code: %w", codeLabel(state.kind), err)
	}
	redirectURL := c.resolveLocation(meta.URL, meta.Location)

	authCode, consumeURL, err := c.walkRedirectChain(ctx, redirectURL)
	if err != nil {
		return "", err
	}
	if consumeURL != "" {
		_ = c.followGet(ctx, consumeURL)
	}
	if authCode == "" {
		return "", errors.New("webapi: failed to get auth code")
	}
	return authCode, nil
}

// finishWithCode exchanges an authorization code at the token endpoint and
// stores the result in galaxy.
func (c *Client) finishWithCode(ctx context.Context, code string) error {
	return c.exchangeCode(ctx, code)
}

// exchangeCode performs the token exchange.
//
// Deliberately separate from auth.Client.Refresh: this is the
// authorization_code grant, while Refresh performs the refresh_token grant.
// They are different protocols that may diverge further (error handling,
// endpoints, client credentials), so they are not merged behind a shared
// helper.
func (c *Client) exchangeCode(ctx context.Context, code string) error {
	q := url.Values{}
	q.Set("client_id", c.galaxy.GetClientID())
	q.Set("client_secret", c.galaxy.GetClientSecret())
	q.Set("grant_type", "authorization_code")
	q.Set("code", code)
	q.Set("redirect_uri", c.galaxy.GetRedirectURI())
	tokenBody, err := c.getResponse(ctx, c.ep.auth+"/token?"+q.Encode())
	if err != nil {
		// This URL carries client_secret and the one-time code, so it is
		// rendered without it (see httpx.SafeError).
		return fmt.Errorf("webapi: token exchange: %s", httpx.SafeError(err))
	}
	token, err := decodeJSONObject(tokenBody)
	if err != nil {
		return fmt.Errorf("webapi: parse token response: %w", err)
	}
	c.galaxy.SetJSON(token)
	return nil
}

// walkRedirectChain follows 3xx redirects manually and extracts the auth code
// from the callback URL. Each step performs a GET
// without auto-redirect. When the response is 3xx the chain continues with
// the new Location; the code is checked on the current URL after each step.
// consumeURL is the final URL for the trailing consume-GET. A 3xx without a
// Location, a non-3xx without a code, and a self-referential redirect all end
// the walk (the last is the only cycle guard; no hop limit is imposed).
func (c *Client) walkRedirectChain(ctx context.Context, redirectURL string) (code, consumeURL string, err error) {
	cur := redirectURL
	for cur != "" {
		meta, err := c.noRedirectGet(ctx, cur)
		if err != nil {
			return "", "", err
		}
		is3xx := meta.StatusCode/100 == 3
		if is3xx {
			next := c.resolveLocation(meta.URL, meta.Location)
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
// redirects and summarises the response.
func (c *Client) postForm(ctx context.Context, target, body string) (responseMeta, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(body))
	if err != nil {
		return responseMeta{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.hx.DoNoRedirect(ctx, req)
	if err != nil {
		return responseMeta{}, err
	}
	return drainResponse(req.URL, resp), nil
}
