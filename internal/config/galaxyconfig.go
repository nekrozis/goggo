package config

import (
	"encoding/json"
	"sync"
	"time"
)

// Galaxy OAuth credentials and redirect URI used by the GOG Galaxy client
// (config.h:206-209). They are the fallback values whenever the stored token
// JSON carries no client_id/client_secret override.
const (
	DefaultClientID     = "46899977096215655"
	DefaultClientSecret = "9d85c43b1482497dbbce61f6e4aa173a433796eeae2ca8c5f6129f2dc4de46d9"
	DefaultRedirectURI  = "https://embed.gog.com/on_login_success?origin=client"
)

// defaultExpiresIn is the token lifetime assumed when the server response
// carries no usable expires_in (config.h:122).
const defaultExpiresIn int64 = 3600

// GalaxyConfig mirrors class GalaxyConfig (config.h:69-213). It guards the
// token JSON with a mutex because token refreshes can run concurrently with
// downloads.
//
// Difference from the C++ class (intentional): GalaxyConfig is used through a
// pointer and created with NewGalaxyConfig; the C++ value-copy/assignment
// semantics are replaced by explicit sharing, which is the idiomatic Go
// approach for a lock-protected credential store.
type GalaxyConfig struct {
	mu          sync.Mutex
	token       map[string]any
	filepath    string
	redirectURI string
}

// NewGalaxyConfig returns a GalaxyConfig with the default redirect URI and an
// empty token store.
func NewGalaxyConfig() *GalaxyConfig {
	return &GalaxyConfig{
		token:       map[string]any{},
		redirectURI: DefaultRedirectURI,
	}
}

// IsExpired reports whether the access token has passed its expires_at.
// A token without expires_at is treated as expired (config.h:75-79).
func (g *GalaxyConfig) IsExpired() bool {
	return g.expiredAt(time.Now().Unix())
}

// expiredAt is the deterministic core of IsExpired.
func (g *GalaxyConfig) expiredAt(now int64) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	exp, ok := jsonInt64(g.token, "expires_at")
	if !ok {
		return true
	}
	return now > exp
}

// GetAccessToken returns the stored access token, or "" when absent
// (config.h:82-89).
func (g *GalaxyConfig) GetAccessToken() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	v, _ := jsonString(g.token, "access_token")
	return v
}

// GetRefreshToken returns the stored refresh token, or "" when absent
// (config.h:91-98).
func (g *GalaxyConfig) GetRefreshToken() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	v, _ := jsonString(g.token, "refresh_token")
	return v
}

// GetUserID returns the stored user id, or "" when absent (config.h:106-114).
func (g *GalaxyConfig) GetUserID() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	v, _ := jsonString(g.token, "user_id")
	return v
}

// GetJSON returns a deep copy of the token store (config.h:100-104). Callers
// may mutate the result without affecting the store.
func (g *GalaxyConfig) GetJSON() map[string]any {
	g.mu.Lock()
	defer g.mu.Unlock()
	return cloneMap(g.token)
}

// SetJSON stores a token response. When the payload lacks expires_at an
// expires_at is derived from expires_in (default 3600 seconds) so expiry can
// be evaluated later without a clock dependency (config.h:116-131).
func (g *GalaxyConfig) SetJSON(token map[string]any) {
	g.setJSONAt(token, time.Now().Unix())
}

// setJSONAt is the deterministic core of SetJSON.
func (g *GalaxyConfig) setJSONAt(token map[string]any, now int64) {
	if _, ok := token["expires_at"]; !ok {
		expiresIn := defaultExpiresIn
		if v, ok := jsonInt64(token, "expires_in"); ok {
			expiresIn = v
		}
		token["expires_at"] = now + expiresIn
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.token = cloneMap(token)
}

// SetFilepath and GetFilepath manage the on-disk location of the token store
// (config.h:133-143).
func (g *GalaxyConfig) SetFilepath(path string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.filepath = path
}

// GetFilepath returns the configured token store path.
func (g *GalaxyConfig) GetFilepath() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.filepath
}

// GetRedirectURI returns the OAuth redirect URI (config.h:175-179).
func (g *GalaxyConfig) GetRedirectURI() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.redirectURI
}

// GetClientID returns a stored client_id override, falling back to the
// Galaxy default (config.h:155-163). An override present in the store is
// returned verbatim, mirroring the C++ asString() behaviour.
func (g *GalaxyConfig) GetClientID() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if v, ok := jsonString(g.token, "client_id"); ok {
		return v
	}
	return DefaultClientID
}

// GetClientSecret returns a stored client_secret override, falling back to
// the Galaxy default (config.h:165-173). An override present in the store is
// returned verbatim.
func (g *GalaxyConfig) GetClientSecret() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if v, ok := jsonString(g.token, "client_secret"); ok {
		return v
	}
	return DefaultClientSecret
}

// ResetClient restores default client_id/client_secret inside the token store
// when the store carries overrides (config.h:145-153). Values missing from
// the store stay absent; the getters already fall back to the defaults.
func (g *GalaxyConfig) ResetClient() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.token["client_id"]; ok {
		g.token["client_id"] = DefaultClientID
	}
	if _, ok := g.token["client_secret"]; ok {
		g.token["client_secret"] = DefaultClientSecret
	}
}

// jsonString reads a string field; ok is false when the key is absent or not
// a JSON string.
func jsonString(m map[string]any, key string) (string, bool) {
	v, ok := m[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

// jsonInt64 reads an integer field tolerant of the numeric types produced by
// encoding/json (float64, json.Number) and native integers.
func jsonInt64(m map[string]any, key string) (int64, bool) {
	v, ok := m[key]
	if !ok || v == nil {
		return 0, false
	}
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case json.Number:
		i, err := n.Int64()
		return i, err == nil
	case int:
		return int64(n), true
	case int64:
		return n, true
	case uint64:
		return int64(n), true
	default:
		return 0, false
	}
}

// cloneMap returns a shallow copy of m. Token values are primitives (strings,
// numbers, arrays of strings), so a shallow copy is sufficient for the
// concurrency contract of GetJSON/SetJSON.
func cloneMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
