package webapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"

	"github.com/nekrozis/goggo/internal/config"
	"github.com/nekrozis/goggo/internal/httpx"
)

// Production GOG hosts used by the C++ source. The endpoint fields are
// overwritable so tests can point the flow at an httptest server.
const (
	DefaultAuthHost  = "https://auth.gog.com"
	DefaultLoginHost = "https://login.gog.com"
	DefaultWWWHost   = "https://www.gog.com"
	DefaultEmbedHost = "https://embed.gog.com"
)

// endpoints groups the per-host URL prefixes of one Client.
type endpoints struct {
	auth  string
	login string
	www   string
	embed string
}

func defaultEndpoints() endpoints {
	return endpoints{auth: DefaultAuthHost, login: DefaultLoginHost, www: DefaultWWWHost, embed: DefaultEmbedHost}
}

// Options carries the login behaviour knobs that came from the C++ global
// config. Force-browser choice is deliberately NOT here: it is a per-login
// policy passed through LoginOptions by the CLI layer.
type Options struct {
	// Retries is the C++ iRetries value. A per-response retry policy of
	// min(3, Retries) additional attempts is derived from it, matching
	// getResponse (website.cpp:33).
	Retries int
}

// Client drives the GOG website login flow over an httpx transport.
//
// Fields are ordered to minimise padding: interfaces and strings (16B each)
// first, then pointers and integers (8B each).
type Client struct {
	galaxy  *config.GalaxyConfig
	hx      *httpx.Client
	retries int
	ep      endpoints
}

// New builds a Client. galaxy is required (nil panics). The httpx config is
// used to construct the transport; its RetryPolicy is replaced by the derived
// login policy (min(3, Retries) additional attempts).
func New(hxCfg httpx.Config, galaxy *config.GalaxyConfig, opts Options) (*Client, error) {
	if galaxy == nil {
		return nil, errors.New("webapi: nil galaxy config")
	}
	extra := opts.Retries
	if extra < 0 {
		extra = 0
	}
	if extra > 3 {
		extra = 3
	}
	hxCfg.RetryPolicy = httpx.RetryPolicy{
		MaxAttempts: extra + 1,
		Wait:        0,
	}
	hx, err := httpx.New(hxCfg)
	if err != nil {
		return nil, fmt.Errorf("webapi: %w", err)
	}
	return &Client{
		galaxy:  galaxy,
		hx:      hx,
		retries: opts.Retries,
		ep:      defaultEndpoints(),
	}, nil
}

// authURL builds the OAuth authorize URL (website.cpp:356).
func (c *Client) authURL() string {
	q := url.Values{}
	q.Set("client_id", c.galaxy.GetClientID())
	q.Set("redirect_uri", c.galaxy.GetRedirectURI())
	q.Set("response_type", "code")
	q.Set("layout", "default")
	q.Set("brand", "gog")
	return c.ep.auth + "/auth?" + q.Encode()
}

// IsLoggedIn probes www.gog.com/account the way IsloggedInSimple does
// (website.cpp:655-695): a direct 200 means logged in; a 3xx redirect to the
// exact embed account URL is followed once and its 200 confirms login.
func (c *Client) IsLoggedIn(ctx context.Context) (bool, error) {
	resp, err := c.noRedirectGet(ctx, c.ep.www+"/account")
	if err != nil {
		return false, err
	}
	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusMovedPermanently, http.StatusFound, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		loc := c.resolveLocation(resp)
		if loc == "" || loc != c.ep.embed+"/account" {
			return false, nil
		}
		resp2, err := c.noRedirectGet(ctx, loc)
		if err != nil {
			return false, err
		}
		return resp2.StatusCode == http.StatusOK, nil
	default:
		return false, nil
	}
}

// noRedirectGet issues a single GET that does not follow redirects and
// drains the body. The caller inspects the returned response.
func (c *Client) noRedirectGet(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.hx.DoNoRedirect(ctx, req)
	if err != nil {
		return nil, err
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp, nil
}

// followGet issues a GET that follows redirects and drains the body,
// mirroring the CURLOPT_FOLLOWLOCATION=1 requests used to consume a callback
// or intermediate page.
func (c *Client) followGet(ctx context.Context, url string) error {
	resp, err := c.hx.Get(ctx, url)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.Body.Close()
}

// resolveLocation turns a redirect response's Location header into an
// absolute URL, or "" when absent. It always resolves relative Locations
// against the request URL (Go's ResolveReference), never via string joining.
func (c *Client) resolveLocation(resp *http.Response) string {
	loc := resp.Header.Get("Location")
	if loc == "" {
		return ""
	}
	ref, err := url.Parse(loc)
	if err != nil {
		return ""
	}
	return resp.Request.URL.ResolveReference(ref).String()
}

// codeRE extracts the OAuth code from a URL. It is an equivalent rewrite of
// the C++ regex ".*code=(.*?)([\?&].*|$)" (website.cpp:584): the value runs
// until the next '&' or '?' or the end of the string. The rewrite is a
// semantic-equivalent port, not a byte-for-byte copy of the expression.
var codeRE = regexp.MustCompile(`(?i)code=([^&?]*)`)

// extractCode returns the auth code embedded in a URL, or "".
func extractCode(s string) string {
	if s == "" {
		return ""
	}
	m := codeRE.FindStringSubmatch(s)
	if m == nil {
		return ""
	}
	return m[1]
}
