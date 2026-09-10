package config

import (
	"encoding/json"
	"math"
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

// GalaxyConfig mirrors class GalaxyConfig (config.h:69-213): a thread-safe
// store for the Galaxy token JSON plus the semantic accessors around it.
//
// Differences from the C++ class (intentional):
//   - Used through *GalaxyConfig and created with NewGalaxyConfig; the C++
//     value-copy semantics are replaced by explicit sharing. Never copy a
//     GalaxyConfig by value and never accept one as a value parameter: sharing
//     the pointer IS the design, and a silent copy would duplicate the lock and
//     the state. Additionally, do not add a String()/formatting method that can
//     dump the raw token — the store holds credentials.
//   - Reads take an RWMutex (C++ uses std::mutex). Not observable.
//   - GetJSON/SetJSON copy deeply and SetJSON never modifies its argument, so a
//     caller cannot alias the store. C++ gets this for free from Json::Value's
//     value semantics.
//
// Unknown JSON fields are preserved verbatim: the store is a JSON tree, not a
// fixed record, so a field the server sends is written back unchanged.
//
// Fields are ordered to minimise padding: RWMutex (24B), then the strings
// (16B each), then the map (8B).
type GalaxyConfig struct {
	mu          sync.RWMutex
	filepath    string
	redirectURI string
	token       map[string]any
}

// NewGalaxyConfig returns a GalaxyConfig with the default redirect URI and an
// empty token store.
func NewGalaxyConfig() *GalaxyConfig {
	return &GalaxyConfig{
		token:       map[string]any{},
		redirectURI: DefaultRedirectURI,
	}
}

// IsExpired reports whether the access token has passed its expires_at; a
// token without a usable expires_at counts as expired (config.h:75-79).
func (g *GalaxyConfig) IsExpired() bool {
	return g.expiredAt(time.Now().Unix())
}

// expiredAt is IsExpired with an injected clock (test seam).
func (g *GalaxyConfig) expiredAt(now int64) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	exp, ok := jsonInt64(g.token, "expires_at")
	if !ok {
		return true
	}
	return now > exp
}

// GetAccessToken returns the stored access token, or "" (config.h:82-89).
func (g *GalaxyConfig) GetAccessToken() string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	v, _ := jsonString(g.token, "access_token")
	return v
}

// GetRefreshToken returns the stored refresh token, or "" (config.h:91-98).
func (g *GalaxyConfig) GetRefreshToken() string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	v, _ := jsonString(g.token, "refresh_token")
	return v
}

// GetUserID returns the stored user id, or "" (config.h:106-114).
func (g *GalaxyConfig) GetUserID() string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	v, _ := jsonString(g.token, "user_id")
	return v
}

// GetJSON returns an independent deep copy of the token store
// (config.h:100-104); callers may mutate the result freely.
func (g *GalaxyConfig) GetJSON() map[string]any {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return cloneJSONMap(g.token)
}

// SetJSON stores a token response without modifying token. When the payload
// lacks expires_at it is derived from expires_in (default 3600) so expiry can
// be evaluated later without a clock dependency (config.h:116-131).
func (g *GalaxyConfig) SetJSON(token map[string]any) {
	g.setJSONAt(token, time.Now().Unix())
}

// setJSONAt is SetJSON with an injected clock (test seam).
func (g *GalaxyConfig) setJSONAt(token map[string]any, now int64) {
	// Clone before deriving expires_at: C++ takes the Json::Value by value, so
	// the caller's object is never touched and the derived field stays private.
	stored := cloneJSONMap(token)
	if _, ok := stored["expires_at"]; !ok {
		expiresIn := defaultExpiresIn
		if v, ok := jsonInt64(stored, "expires_in"); ok {
			expiresIn = v
		}
		stored["expires_at"] = now + expiresIn
	}
	g.mu.Lock()
	g.token = stored
	g.mu.Unlock()
}

// SetFilepath sets the on-disk location of the token store (config.h:133-143).
func (g *GalaxyConfig) SetFilepath(path string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.filepath = path
}

// GetFilepath returns the configured token store path.
func (g *GalaxyConfig) GetFilepath() string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.filepath
}

// GetRedirectURI returns the OAuth redirect URI (config.h:175-179).
func (g *GalaxyConfig) GetRedirectURI() string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.redirectURI
}

// GetClientID returns a stored client_id override verbatim, falling back to
// the Galaxy default (config.h:155-163).
func (g *GalaxyConfig) GetClientID() string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if v, ok := jsonString(g.token, "client_id"); ok {
		return v
	}
	return DefaultClientID
}

// GetClientSecret returns a stored client_secret override verbatim, falling
// back to the Galaxy default (config.h:165-173).
func (g *GalaxyConfig) GetClientSecret() string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if v, ok := jsonString(g.token, "client_secret"); ok {
		return v
	}
	return DefaultClientSecret
}

// ResetClient restores default client_id/client_secret when the store carries
// overrides; absent keys stay absent, since the getters already fall back
// (config.h:145-153).
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

// jsonInt64 reads an integer field, tolerating the numeric types
// encoding/json produces (float64, json.Number) plus native integers. Numbers
// that cannot be represented as int64 are reported as absent rather than
// converted: Go's float→int conversion is implementation-defined for them, and
// a malformed expires_in/expires_at must not fabricate a value.
func jsonInt64(m map[string]any, key string) (int64, bool) {
	v, ok := m[key]
	if !ok || v == nil {
		return 0, false
	}
	switch n := v.(type) {
	case float64:
		return floatToInt64(n)
	case json.Number:
		i, err := n.Int64()
		return i, err == nil
	case int:
		return int64(n), true
	case int64:
		return n, true
	case uint64:
		if n > math.MaxInt64 {
			return 0, false
		}
		return int64(n), true
	default:
		return 0, false
	}
}

// floatToInt64 converts a JSON number, rejecting NaN, infinities and values
// outside [MinInt64, MaxInt64). The bounds are compared as floats because
// float64(2^63) rounds to exactly 2^63, which is already out of range.
func floatToInt64(f float64) (int64, bool) {
	const maxInt64Exclusive = float64(1 << 63)
	if math.IsNaN(f) || math.IsInf(f, 0) || f >= maxInt64Exclusive || f < -maxInt64Exclusive {
		return 0, false
	}
	return int64(f), true
}

// cloneJSONMap deep-copies a token map. Only the shapes encoding/json produces
// are copied (map[string]any, []any); scalars are returned unchanged, so
// numeric types survive exactly (an int64 expires_at stays int64) and unknown
// fields are preserved.
func cloneJSONMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = cloneJSONValue(v)
	}
	return out
}

// cloneJSONValue is the recursive core of cloneJSONMap. Non-JSON containers are
// shared by reference, which is safe because the store only ever holds JSON
// shapes.
func cloneJSONValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return cloneJSONMap(t)
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = cloneJSONValue(e)
		}
		return out
	default:
		return v
	}
}
