package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/nekrozis/goggo/internal/httpx"
)

// defaultTokenURL is the OAuth token endpoint used by refresh (and login).
// Each Client carries its own copy so tests can point one instance at an
// httptest server without any package-level mutable state.
const defaultTokenURL = "https://auth.gog.com/token"

// Client performs the HTTP-backed part of the auth lifecycle. Refresh is a
// method because it needs an httpx transport and a token endpoint; token-file
// operations live on Store because they are stateful.
type Client struct {
	tokenURL string
	hx       *httpx.Client
}

// NewClient builds an auth Client using hx for HTTP. The token URL defaults
// to the production endpoint; tests override the unexported field.
func NewClient(hx *httpx.Client) *Client {
	return &Client{hx: hx, tokenURL: defaultTokenURL}
}

// refresh renews the access token through the store and stores the response.
// The request asks for a new session (no without_new_session parameter); the
// response is stored with client_id/client_secret injected. It NEVER persists to
// disk: the caller decides when to call Store.Save, because a refresh that
// succeeded in memory and then failed to write is a different outcome from one
// that never happened.
//
// Any non-empty JSON object response counts as success, so callers must not
// assume a successful refresh implies an access_token is present.
func (c *Client) refresh(ctx context.Context, s *Store, newSession bool) error {
	refreshToken := s.refreshToken()
	if refreshToken == "" {
		return errors.New("auth: no refresh token stored")
	}
	clientID, clientSecret := s.ClientID(), s.ClientSecret()

	obj, err := c.refreshRequest(ctx, refreshToken, clientID, clientSecret, newSession)
	if err != nil {
		return err
	}
	// Inject the client credentials used for the request before storing.
	obj["client_id"] = clientID
	obj["client_secret"] = clientSecret
	s.StoreLoginResponse(obj)
	return nil
}

// refreshRequest performs one refresh-token grant and returns the decoded
// response object.
func (c *Client) refreshRequest(ctx context.Context, refreshToken, clientID, clientSecret string, newSession bool) (map[string]any, error) {
	q := url.Values{}
	q.Set("client_id", clientID)
	q.Set("client_secret", clientSecret)
	q.Set("grant_type", "refresh_token")
	q.Set("refresh_token", refreshToken)
	if !newSession {
		q.Set("without_new_session", "1")
	}
	body, err := c.hx.GetBytesWithRetry(ctx, c.tokenURL+"?"+q.Encode())
	if err != nil {
		// This URL carries client_secret and refresh_token, so it is rendered
		// without it (see httpx.SafeError).
		return nil, fmt.Errorf("auth: refresh token: %s", httpx.SafeError(err))
	}
	obj, err := decodeObject(string(body))
	if err != nil {
		return nil, fmt.Errorf("auth: refresh token: %w", err)
	}
	return obj, nil
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
