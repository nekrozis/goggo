package webapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"

	"github.com/nekrozis/goggo/internal/httpx"
)

// Production GOG hosts. The endpoint fields are overwritable so tests can point
// the flow at an httptest server.
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

// ClientIdentity is what the website client needs from the credential seam:
// the OAuth client identity plus the one write path for a login response. The
// reset restores the default identity before a login, so a stored override
// cannot leak into a fresh authorization.
type ClientIdentity interface {
	ClientID() string
	ClientSecret() string
	RedirectURI() string
	ResetClient()
	StoreLoginResponse(map[string]any)
}

// Client drives the GOG website login flow over an httpx transport.
type Client struct {
	ep     endpoints
	galaxy ClientIdentity
	hx     *httpx.Client
}

// New builds a Client on a caller-provided transport. galaxy is required, and so
// is the client: a nil one is an error.
//
// The transport is owned by the caller, so the same *httpx.Client — and with it
// the same cookie jar — serves the login flow and session persistence
// (LoadCookies/SaveCookies). Retry behaviour is the caller's too, see
// RetryPolicyFor.
func New(hx *httpx.Client, galaxy ClientIdentity) (*Client, error) {
	if galaxy == nil {
		return nil, errors.New("webapi: nil galaxy config")
	}
	if hx == nil {
		return nil, errors.New("webapi: nil http client")
	}
	return &Client{
		ep:     defaultEndpoints(),
		galaxy: galaxy,
		hx:     hx,
	}, nil
}

// authURL builds the OAuth authorize URL.
func (c *Client) authURL() string {
	q := url.Values{}
	q.Set("client_id", c.galaxy.ClientID())
	q.Set("redirect_uri", c.galaxy.RedirectURI())
	q.Set("response_type", "code")
	q.Set("layout", "default")
	q.Set("brand", "gog")
	return c.ep.auth + "/auth?" + q.Encode()
}

// IsLoggedIn probes www.gog.com/account: a direct 200 means logged in; a 3xx
// redirect to the exact embed account URL is followed once and its 200 confirms
// login.
func (c *Client) IsLoggedIn(ctx context.Context) (bool, error) {
	meta, err := c.noRedirectGet(ctx, c.ep.www+"/account")
	if err != nil {
		return false, err
	}
	switch meta.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusMovedPermanently, http.StatusFound, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		loc := c.resolveLocation(meta.URL, meta.Location)
		if loc == "" || loc != c.ep.embed+"/account" {
			return false, nil
		}
		meta2, err := c.noRedirectGet(ctx, loc)
		if err != nil {
			return false, err
		}
		return meta2.StatusCode == http.StatusOK, nil
	default:
		return false, nil
	}
}

// responseMeta summarises a request whose body has already been drained and
// closed. It replaces handing callers an *http.Response with an exhausted
// body, and it avoids relying on http.Response.Request: URL is the request
// URL parsed when the request was built, while Location stays the raw header
// value (resolution is centralised in resolveLocation).
type responseMeta struct {
	Location   string
	URL        *url.URL
	StatusCode int
}

// drainResponse consumes and closes resp.Body and returns the summary bound
// to reqURL, the URL the request was constructed from.
func drainResponse(reqURL *url.URL, resp *http.Response) responseMeta {
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return responseMeta{
		Location:   resp.Header.Get("Location"),
		URL:        reqURL,
		StatusCode: resp.StatusCode,
	}
}

// noRedirectGet issues a single GET that does not follow redirects, drains
// the body and summarises the response for the caller.
func (c *Client) noRedirectGet(ctx context.Context, target string) (responseMeta, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return responseMeta{}, httpx.SanitizeError(err)
	}
	resp, err := c.hx.DoNoRedirect(ctx, req)
	if err != nil {
		return responseMeta{}, httpx.SanitizeError(err)
	}
	return drainResponse(req.URL, resp), nil
}

// followGet issues a GET that follows redirects and drains the body, used to
// consume a callback or intermediate page.
func (c *Client) followGet(ctx context.Context, target string) error {
	resp, err := c.hx.Get(ctx, target)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.Body.Close()
}

// resolveLocation turns a Location header into an absolute URL, or "" when it
// is absent or unparsable. base is the URL the response's request was built
// from; relative Locations are resolved against it (Go's ResolveReference),
// never via string joining.
func (c *Client) resolveLocation(base *url.URL, location string) string {
	if location == "" || base == nil {
		return ""
	}
	ref, err := url.Parse(location)
	if err != nil {
		return ""
	}
	return base.ResolveReference(ref).String()
}

// codeRE extracts the OAuth code from a URL: the value runs until the next '&'
// or '?' or the end of the string.
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
