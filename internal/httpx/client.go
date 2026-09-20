package httpx

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"time"
)

// Config configures a Client. Transport-level concerns only: retry behaviour
// is a RetryPolicy carried by the config and applied by GetBytesWithRetry
// (and available generically through DoWithRetry); policies are not baked
// into Do/Get.
type Config struct {
	// RetryPolicy is the default retry behaviour for GetBytesWithRetry.
	// A zero MaxAttempts yields a single attempt (no retry). Business layers
	// that need bespoke behaviour (e.g. OAuth 401-refresh) compose
	// DoWithRetry with their own policy instead.
	RetryPolicy RetryPolicy

	// UserAgent is sent on every request when set.
	UserAgent string

	// CACertPath is an optional PEM bundle appended to the system roots.
	// Empty keeps the system roots.
	CACertPath string

	// CookieFile enables cookie-file persistence when non-empty: New installs
	// a cookieStore as the transport's jar, and LoadCookies/SaveCookies read and
	// write this path. New performs no file I/O itself, so the caller loads the
	// state explicitly, before its session checks.
	//
	// A non-empty CookieFile with a caller-provided HTTPClient is an error
	// (ErrCookieFileUnsupported): the caller's jar cannot be replaced.
	CookieFile string

	// Transport optionally replaces the network exit of the client this
	// package builds: the cookie jar and CookieFile persistence, the retry
	// policy and the low-speed guard all stay this package's decisions.
	//
	// It is ignored when HTTPClient is set, and the TLS settings and connect
	// timeout describe the default transport only, so they are not applied to a
	// replacement.
	Transport http.RoundTripper

	// HTTPClient optionally overrides the underlying client; nil builds one
	// from the other settings.
	//
	// Prefer Transport when only the network exit has to change: a
	// caller-provided client also decides the jar, so it cannot be combined
	// with CookieFile.
	HTTPClient *http.Client

	// Timeout bounds the TCP connect; the zero value leaves the default dial
	// timeout.
	Timeout time.Duration

	// LowSpeedLimit is the rate in bytes per second below which a transfer
	// may be aborted with ErrLowSpeed; LowSpeedTime is how long the average
	// rate may stay below it. A zero value selects the transport default, so
	// the guard cannot be switched off by an accidental zero;
	// DisableLowSpeedGuard is the explicit off switch.
	LowSpeedLimit int64
	LowSpeedTime  time.Duration

	// InsecureSkipVerify disables TLS certificate verification. It exists for
	// behaviour compatibility with the downloader's "do not verify" setting,
	// not as a recommended mode; the zero value (verify) is the secure
	// default. The CLI layer maps a disabled verify setting to true here.
	InsecureSkipVerify bool

	// DisableLowSpeedGuard turns the low-speed watchdog off entirely, for
	// callers and tests that need a transport without a transfer guard.
	DisableLowSpeedGuard bool
}

// Client is the transport layer. The embedded *http.Client carries connection
// pooling, redirect handling, TLS, timeouts and a cookie jar that lives for the
// whole Client lifetime, so a cookie received by one request (a login) is sent
// with later ones. store is non-nil only when Config.CookieFile is set and the
// client was built here; then the jar is a *cookieStore, which also maintains
// reconstructable persistence state.
type Client struct {
	policy        RetryPolicy
	ua            string
	cookieFile    string
	hc            *http.Client
	store         *cookieStore
	lowSpeedTime  time.Duration
	lowSpeedLimit int64
	lowSpeedGuard bool
}

// New builds a Client from cfg. It performs no file I/O: cookie persistence
// is loaded explicitly through LoadCookies.
func New(cfg Config) (*Client, error) {
	policy := cfg.RetryPolicy
	if policy.MaxAttempts < 1 {
		policy.MaxAttempts = 1
	}
	if policy.ShouldRetry == nil {
		policy.ShouldRetry = DefaultShouldRetry
	}

	// The low-speed guard: the transport owns the defaults so an unconfigured
	// caller still gets a guard, and the explicit switch is the only way to
	// turn it off.
	guard := !cfg.DisableLowSpeedGuard
	limit, window := cfg.LowSpeedLimit, cfg.LowSpeedTime
	if limit <= 0 {
		limit = DefaultLowSpeedLimit
	}
	if window <= 0 {
		window = DefaultLowSpeedTime
	}

	hc := cfg.HTTPClient
	var store *cookieStore
	if hc == nil {
		tlsConfig := &tls.Config{InsecureSkipVerify: cfg.InsecureSkipVerify} //nolint:gosec // opt-in, see InsecureSkipVerify
		if cfg.CACertPath != "" {
			pool, err := x509.SystemCertPool()
			if err != nil || pool == nil {
				pool = x509.NewCertPool()
			}
			pem, err := os.ReadFile(cfg.CACertPath)
			if err != nil {
				return nil, fmt.Errorf("read CA cert %q: %w", cfg.CACertPath, err)
			}
			if !pool.AppendCertsFromPEM(pem) {
				return nil, fmt.Errorf("no certificates parsed from %q", cfg.CACertPath)
			}
			tlsConfig.RootCAs = pool
		}

		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.TLSClientConfig = tlsConfig
		if cfg.Timeout > 0 {
			transport.DialContext = (&net.Dialer{Timeout: cfg.Timeout}).DialContext
		}

		// The replacement only takes the network exit: the client, its jar and
		// the redirect policy stay this package's, which is what lets a caller
		// keep cookie persistence while pointing the production hosts elsewhere.
		var exit http.RoundTripper = transport
		if cfg.Transport != nil {
			exit = cfg.Transport
		}

		// http.Client follows redirects by default.
		hc = &http.Client{Transport: exit}
		if cfg.CookieFile != "" {
			store = newCookieStore()
			hc.Jar = store
		} else {
			jar, err := cookiejar.New(nil)
			if err != nil {
				return nil, fmt.Errorf("create cookie jar: %w", err)
			}
			hc.Jar = jar
		}
	}

	return &Client{
		policy:        policy,
		ua:            cfg.UserAgent,
		cookieFile:    cfg.CookieFile,
		hc:            hc,
		store:         store,
		lowSpeedTime:  window,
		lowSpeedLimit: limit,
		lowSpeedGuard: guard,
	}, nil
}

// guardBody installs the low-speed watchdog on a response. Every response that
// leaves this package goes through it (Do and DoNoRedirect), so every caller —
// webapi, auth, galaxy and the download path — inherits the guard without
// implementing anything themselves.
func (c *Client) guardBody(resp *http.Response) {
	if !c.lowSpeedGuard || resp == nil || resp.Body == nil {
		return
	}
	resp.Body = newLowSpeedBody(resp.Body, c.lowSpeedLimit, c.lowSpeedTime)
}

// Do performs a single request (no retry). ctx is honored through the
// request context; the User-Agent is added when cfg.UserAgent is set and the
// request carries none. The caller owns the returned response and must close
// its body.
func (c *Client) Do(ctx context.Context, req *http.Request) (*http.Response, error) {
	req = req.WithContext(ctx)
	if c.ua != "" && req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", c.ua)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		// The transport reports a broken connection as a *url.Error carrying the
		// request URL, and the signed download URLs carry a session token.
		return nil, SanitizeError(err)
	}
	c.guardBody(resp)
	return resp, nil
}

// DoNoRedirect performs a single request (no retry) and returns the response
// WITHOUT following redirects: a 3xx is returned as-is so the caller can
// inspect the Location header and drive the chain itself. This is a per-call
// behaviour, not a client-wide mode switch — other requests through the same
// Client still follow redirects.
func (c *Client) DoNoRedirect(ctx context.Context, req *http.Request) (*http.Response, error) {
	req = req.WithContext(ctx)
	if c.ua != "" && req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", c.ua)
	}
	oneShot := *c.hc
	oneShot.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := oneShot.Do(req)
	if err != nil {
		return nil, SanitizeError(err)
	}
	c.guardBody(resp)
	return resp, nil
}

// Get performs a single GET request (no retry) and returns the raw response.
func (c *Client) Get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		// An unparsable URL is reported as a *url.Error holding that URL.
		return nil, SanitizeError(err)
	}
	return c.Do(ctx, req)
}

// GetBytes is the small-response convenience wrapper over Get: it performs a
// single GET, reads the whole body and returns it. Responses with status
// >= 400 produce a *StatusError. Big transfers must stream from the raw
// *http.Response instead.
func (c *Client) GetBytes(ctx context.Context, url string) ([]byte, error) {
	resp, err := c.Get(ctx, url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, NewStatusError(http.MethodGet, url, resp.StatusCode)
	}
	return body, nil
}

// GetBytesWithRetry runs a GET under the client's default RetryPolicy and
// reads the final body. Statuses >= 400 on the final response produce a
// *StatusError.
func (c *Client) GetBytesWithRetry(ctx context.Context, url string) ([]byte, error) {
	resp, err := DoWithRetry(ctx, c.policy, func(ctx context.Context) (*http.Response, error) {
		return c.Get(ctx, url)
	})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, NewStatusError(http.MethodGet, url, resp.StatusCode)
	}
	return body, nil
}

// DoBytesWithRetry runs req under the client's default RetryPolicy and reads
// the final body: the request-carrying counterpart of GetBytesWithRetry, for a
// caller that needs its own headers (the Galaxy content endpoints send
// "Authorization: Bearer <token>").
//
// req must be re-issuable, because the policy may send it more than once: a nil
// body or a request carrying GetBody is fine, a one-shot stream is not. A final
// status >= 400 produces a *StatusError.
func (c *Client) DoBytesWithRetry(ctx context.Context, req *http.Request) ([]byte, error) {
	if req == nil {
		return nil, errors.New("httpx: nil request")
	}
	resp, err := DoWithRetry(ctx, c.policy, func(ctx context.Context) (*http.Response, error) {
		return c.Do(ctx, req)
	})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, NewStatusError(req.Method, req.URL.String(), resp.StatusCode)
	}
	return body, nil
}
