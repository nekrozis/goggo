package config

import (
	"encoding/json"
	"math"
	"reflect"
	"sync"
	"testing"
)

func TestSetJSONFillsExpiresAtWithDefaultTTL(t *testing.T) {
	tok := map[string]any{}
	g := NewGalaxyConfig()
	g.setJSONAt(tok, 1000)
	exp, ok := jsonInt64(g.GetJSON(), "expires_at")
	if !ok || exp != 1000+3600 {
		t.Fatalf("expires_at = %v, want %d", exp, 1000+3600)
	}
	if _, exists := tok["expires_at"]; exists {
		t.Errorf("SetJSON must not modify the caller's map: %v", tok)
	}
}

func TestSetJSONUsesServerExpiresIn(t *testing.T) {
	tok := map[string]any{"expires_in": float64(120)}
	g := NewGalaxyConfig()
	g.setJSONAt(tok, 1000)
	exp, _ := jsonInt64(g.GetJSON(), "expires_at")
	if exp != 1120 {
		t.Fatalf("expires_at = %d, want 1120", exp)
	}
	if _, exists := tok["expires_at"]; exists {
		t.Errorf("SetJSON must not modify the caller's map: %v", tok)
	}
}

func TestSetJSONNullExpiresInFallsBackToDefault(t *testing.T) {
	tok := map[string]any{"expires_in": nil}
	g := NewGalaxyConfig()
	g.setJSONAt(tok, 1000)
	exp, _ := jsonInt64(g.GetJSON(), "expires_at")
	if exp != 1000+3600 {
		t.Fatalf("expires_at = %d, want %d", exp, 1000+3600)
	}
	if _, exists := tok["expires_at"]; exists {
		t.Errorf("SetJSON must not modify the caller's map: %v", tok)
	}
}

func TestSetJSONKeepsExistingExpiresAt(t *testing.T) {
	tok := map[string]any{"expires_at": float64(424242)}
	g := NewGalaxyConfig()
	g.setJSONAt(tok, 1000)
	exp, _ := jsonInt64(g.GetJSON(), "expires_at")
	if exp != 424242 {
		t.Fatalf("expires_at = %d, want 424242", exp)
	}
	// Two-layer assertion: the caller's own value must be untouched too.
	if got := tok["expires_at"]; got != float64(424242) {
		t.Errorf("caller expires_at = %v, want the original float64(424242)", got)
	}
	if n := len(tok); n != 1 {
		t.Errorf("caller map size = %d, want 1", n)
	}
}

func TestIsExpiredBoundary(t *testing.T) {
	g := NewGalaxyConfig()
	g.setJSONAt(map[string]any{"expires_at": float64(1000)}, 0)
	if g.expiredAt(999) {
		t.Error("expiredAt(999): want false (now < expires_at)")
	}
	if g.expiredAt(1000) {
		t.Error("expiredAt(1000): want false (now == expires_at)")
	}
	if !g.expiredAt(1001) {
		t.Error("expiredAt(1001): want true (now > expires_at)")
	}
}

func TestIsExpiredWithoutExpiresAt(t *testing.T) {
	g := NewGalaxyConfig()
	if !g.expiredAt(12345) {
		t.Error("token without expires_at must be treated as expired")
	}
}

func TestTokenGettersEmptyWithoutToken(t *testing.T) {
	g := NewGalaxyConfig()
	if v := g.GetAccessToken(); v != "" {
		t.Errorf("GetAccessToken() = %q, want empty", v)
	}
	if v := g.GetRefreshToken(); v != "" {
		t.Errorf("GetRefreshToken() = %q, want empty", v)
	}
	if v := g.GetUserID(); v != "" {
		t.Errorf("GetUserID() = %q, want empty", v)
	}
	if v := g.GetClientID(); v != DefaultClientID {
		t.Errorf("GetClientID() = %q, want default", v)
	}
	if v := g.GetClientSecret(); v != DefaultClientSecret {
		t.Errorf("GetClientSecret() = %q, want default", v)
	}
	if v := g.GetRedirectURI(); v != DefaultRedirectURI {
		t.Errorf("GetRedirectURI() = %q, want default", v)
	}
}

func TestTokenGettersReturnStoredValues(t *testing.T) {
	g := NewGalaxyConfig()
	g.SetJSON(map[string]any{
		"access_token":  "a-token",
		"refresh_token": "r-token",
		"user_id":       "u-1",
		"client_id":     "my-client",
		"client_secret": "my-secret",
	})
	if v := g.GetAccessToken(); v != "a-token" {
		t.Errorf("access token = %q", v)
	}
	if v := g.GetRefreshToken(); v != "r-token" {
		t.Errorf("refresh token = %q", v)
	}
	if v := g.GetUserID(); v != "u-1" {
		t.Errorf("user id = %q", v)
	}
	if v := g.GetClientID(); v != "my-client" {
		t.Errorf("client id = %q", v)
	}
	if v := g.GetClientSecret(); v != "my-secret" {
		t.Errorf("client secret = %q", v)
	}
}

func TestEmptyClientOverrideReturnedVerbatim(t *testing.T) {
	// An existing (even empty) member is returned as-is.
	g := NewGalaxyConfig()
	g.SetJSON(map[string]any{"client_id": ""})
	if v := g.GetClientID(); v != "" {
		t.Errorf("GetClientID() = %q, want empty override", v)
	}
}

func TestResetClientRestoresDefaults(t *testing.T) {
	g := NewGalaxyConfig()
	g.SetJSON(map[string]any{
		"client_id":     "custom-id",
		"client_secret": "custom-secret",
	})
	g.ResetClient()
	if v := g.GetClientID(); v != DefaultClientID {
		t.Errorf("after ResetClient client id = %q, want default", v)
	}
	if v := g.GetClientSecret(); v != DefaultClientSecret {
		t.Errorf("after ResetClient client secret = %q, want default", v)
	}
}

func TestGetJSONReturnsIsolatedCopy(t *testing.T) {
	g := NewGalaxyConfig()
	g.SetJSON(map[string]any{"access_token": "tok", "expires_at": float64(500)})
	got := g.GetJSON()
	got["access_token"] = "mutated"
	got["expires_at"] = float64(1)
	if v := g.GetAccessToken(); v != "tok" {
		t.Errorf("store mutated through GetJSON copy: %q", v)
	}
}

func TestFilepathAccessors(t *testing.T) {
	g := NewGalaxyConfig()
	if v := g.GetFilepath(); v != "" {
		t.Errorf("GetFilepath() = %q, want empty", v)
	}
	g.SetFilepath("/tmp/tokens.json")
	if v := g.GetFilepath(); v != "/tmp/tokens.json" {
		t.Errorf("GetFilepath() = %q", v)
	}
}

func TestGalaxyConfigConcurrentAccess(t *testing.T) {
	g := NewGalaxyConfig()
	g.SetJSON(map[string]any{
		"access_token":  "tok",
		"refresh_token": "ref",
		"expires_in":    float64(3600),
	})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				g.IsExpired()
				g.GetAccessToken()
				g.GetClientID()
				g.GetJSON()
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 200; j++ {
			g.SetJSON(map[string]any{
				"access_token": "tok-new",
				"expires_in":   float64(3600),
				"client_id":    "custom",
			})
			g.ResetClient()
		}
	}()
	wg.Wait()
	if g.GetAccessToken() != "tok-new" {
		t.Errorf("final access token = %q, want tok-new", g.GetAccessToken())
	}
	// The writer's last call was ResetClient, so the override is gone.
	if v := g.GetClientID(); v != DefaultClientID {
		t.Errorf("final client id = %q, want the default", v)
	}
}

// TestSetJSONNeverMutatesCallerMap covers every injection path: the caller's
// map must come back unchanged.
func TestSetJSONNeverMutatesCallerMap(t *testing.T) {
	cases := []struct {
		name string
		tok  map[string]any
	}{
		{"empty", map[string]any{}},
		{"expires_in", map[string]any{"expires_in": float64(120)}},
		{"null expires_in", map[string]any{"expires_in": nil}},
		{"existing expires_at", map[string]any{"expires_at": float64(424242)}},
		{"nested containers", map[string]any{"nested": map[string]any{"list": []any{"a"}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := cloneJSONMap(tc.tok)
			g := NewGalaxyConfig()
			g.SetJSON(tc.tok)
			if !reflect.DeepEqual(tc.tok, before) {
				t.Errorf("caller map mutated:\nbefore=%v\nafter =%v", before, tc.tok)
			}
		})
	}
}

// TestGetJSONDeepCopiesNestedContainers locks the output side: mutating a
// nested container of the returned map must not reach the store.
func TestGetJSONDeepCopiesNestedContainers(t *testing.T) {
	g := NewGalaxyConfig()
	g.SetJSON(map[string]any{
		"access_token": "tok",
		"nested":       map[string]any{"list": []any{"a", "b"}},
	})
	got := g.GetJSON()
	nested, ok := got["nested"].(map[string]any)
	if !ok {
		t.Fatalf("nested = %T, want map[string]any", got["nested"])
	}
	list, ok := nested["list"].([]any)
	if !ok || len(list) == 0 {
		t.Fatalf("nested list = %#v, want a non-empty []any", nested["list"])
	}
	list[0] = "mutated"
	nested["extra"] = true

	want := map[string]any{"list": []any{"a", "b"}}
	if again := g.GetJSON(); !reflect.DeepEqual(again["nested"], want) {
		t.Errorf("store mutated through the GetJSON copy: %v", again["nested"])
	}
}

// TestSetJSONPreservesValueTypes: cloning copies structure, not scalars, so
// numeric types survive (an int64 expires_at stays int64). A JSON
// marshal/unmarshal round trip would silently turn it into float64 and break
// auth's token-file assertions.
func TestSetJSONPreservesValueTypes(t *testing.T) {
	g := NewGalaxyConfig()
	g.SetJSON(map[string]any{"expires_at": int64(7), "count": 3})
	got := g.GetJSON()
	if v, ok := got["expires_at"].(int64); !ok || v != 7 {
		t.Errorf("expires_at = %T %v, want int64 7", got["expires_at"], got["expires_at"])
	}
	if v, ok := got["count"].(int); !ok || v != 3 {
		t.Errorf("count = %T %v, want int 3", got["count"], got["count"])
	}
}

// TestJSONInt64Boundaries covers the D1 hardening: numbers that cannot be
// represented as int64 are reported as absent instead of converted. -2^63 is
// the accepted lower bound, +2^63 the rejected upper one.
func TestJSONInt64Boundaries(t *testing.T) {
	cases := []struct {
		name   string
		in     any
		want   int64
		wantOK bool
	}{
		{"int64", int64(42), 42, true},
		{"int", 42, 42, true},
		{"float64 integral", float64(42), 42, true},
		{"json.Number", json.Number("42"), 42, true},
		{"json.Number invalid", json.Number("4x"), 0, false},
		{"uint64 max int64", uint64(math.MaxInt64), math.MaxInt64, true},
		{"uint64 overflow", uint64(math.MaxInt64) + 1, 0, false},
		{"max uint64", uint64(math.MaxUint64), 0, false},
		{"min int64 exact", math.Ldexp(-1, 63), math.MinInt64, true},
		{"2^63 rejected", math.Ldexp(1, 63), 0, false},
		{"2^64 rejected", math.Ldexp(1, 64), 0, false},
		{"below int64 rejected", -math.Ldexp(1, 64), 0, false},
		{"NaN", math.NaN(), 0, false},
		{"+Inf", math.Inf(1), 0, false},
		{"-Inf", math.Inf(-1), 0, false},
		{"nil", nil, 0, false},
		{"string", "42", 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := jsonInt64(map[string]any{"v": tc.in}, "v")
			if ok != tc.wantOK || (ok && got != tc.want) {
				t.Errorf("jsonInt64(%v) = (%d, %v), want (%d, %v)", tc.in, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

// TestNonFiniteExpiresInFallsBackToDefaultTTL: an unusable expires_in is
// treated as absent, so the default TTL applies (D1).
func TestNonFiniteExpiresInFallsBackToDefaultTTL(t *testing.T) {
	for _, bad := range []any{math.NaN(), math.Inf(1), math.Inf(-1), math.Ldexp(1, 64)} {
		g := NewGalaxyConfig()
		g.setJSONAt(map[string]any{"expires_in": bad}, 1000)
		exp, ok := jsonInt64(g.GetJSON(), "expires_at")
		if !ok || exp != 1000+defaultExpiresIn {
			t.Errorf("expires_in %v: expires_at = (%d, %v), want %d", bad, exp, ok, 1000+defaultExpiresIn)
		}
	}
}

// TestNonFiniteExpiresAtCountsAsExpired: an unusable expires_at cannot show the
// token to be valid, so it counts as expired (D1).
func TestNonFiniteExpiresAtCountsAsExpired(t *testing.T) {
	for _, bad := range []any{math.NaN(), math.Inf(1), math.Inf(-1), math.Ldexp(1, 64), "not-a-number"} {
		g := NewGalaxyConfig()
		g.setJSONAt(map[string]any{"expires_at": bad}, 0)
		if !g.expiredAt(1) {
			t.Errorf("expires_at %v: expiredAt(1) = false, want true", bad)
		}
	}
}
