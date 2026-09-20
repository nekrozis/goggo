package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/nekrozis/goggo/internal/config"
	"github.com/nekrozis/goggo/internal/httpx"
)

// defaultTokenURL is the OAuth token endpoint used by refresh (and login).
// Each Client carries its own copy so tests can point one instance at an
// httptest server without any package-level mutable state.
const defaultTokenURL = "https://auth.gog.com/token"

// Client performs the HTTP-backed part of the auth lifecycle. Refresh is a
// method because it needs an httpx transport and a token endpoint; token-file
// operations are package functions because they are stateless.
type Client struct {
	tokenURL string
	hx       *httpx.Client
}

// NewClient builds an auth Client using hx for HTTP. The token URL defaults
// to the production endpoint; tests override the unexported field.
func NewClient(hx *httpx.Client) *Client {
	return &Client{hx: hx, tokenURL: defaultTokenURL}
}

// Refresh renews the Galaxy access token using the stored refresh token. The request
// asks for a new session (no without_new_session parameter); on success the response
// is stored into g with client_id/client_secret injected. It NEVER persists to disk:
// the caller decides when to call SaveTokenFile.
//
// Any non-empty JSON object response counts as success, so callers must not assume a
// successful Refresh implies an access_token is present.
func (c *Client) Refresh(ctx context.Context, g *config.GalaxyConfig) error {
	return c.refreshSession(ctx, g, true)
}

// refreshSession is the core of Refresh. newSession is deliberately NOT part of
// the public API; expose a semantic option later only if a caller actually needs
// the without_new_session variant.
func (c *Client) refreshSession(ctx context.Context, g *config.GalaxyConfig, newSession bool) error {
	refreshToken := g.GetRefreshToken()
	if refreshToken == "" {
		return errors.New("auth: no refresh token stored")
	}

	q := url.Values{}
	q.Set("client_id", g.GetClientID())
	q.Set("client_secret", g.GetClientSecret())
	q.Set("grant_type", "refresh_token")
	q.Set("refresh_token", refreshToken)
	if !newSession {
		q.Set("without_new_session", "1")
	}
	body, err := c.hx.GetBytesWithRetry(ctx, c.tokenURL+"?"+q.Encode())
	if err != nil {
		// This URL carries client_secret and refresh_token, so it is rendered
		// without it (see httpx.SafeError).
		return fmt.Errorf("auth: refresh token: %s", httpx.SafeError(err))
	}
	obj, err := decodeObject(string(body))
	if err != nil {
		return fmt.Errorf("auth: refresh token: %w", err)
	}

	// Inject the client credentials used for the request before storing.
	obj["client_id"] = g.GetClientID()
	obj["client_secret"] = g.GetClientSecret()
	g.SetJSON(obj)
	return nil
}

// decodeObject decodes a JSON object body. An empty body or a non-object
// JSON value is an error.
func decodeObject(body string) (map[string]any, error) {
	if strings.TrimSpace(body) == "" {
		return nil, errors.New("empty JSON response")
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(body), &obj); err != nil {
		return nil, err
	}
	return obj, nil
}
