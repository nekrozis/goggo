package config

import (
	"sync"
	"testing"
)

func TestSetJSONFillsExpiresAtWithDefaultTTL(t *testing.T) {
	tok := map[string]any{}
	g := NewGalaxyConfig()
	g.setJSONAt(tok, 1000)
	exp, ok := jsonInt64(tok, "expires_at")
	if !ok || exp != 1000+3600 {
		t.Fatalf("expires_at = %v, want %d", exp, 1000+3600)
	}
}

func TestSetJSONUsesServerExpiresIn(t *testing.T) {
	tok := map[string]any{"expires_in": float64(120)}
	g := NewGalaxyConfig()
	g.setJSONAt(tok, 1000)
	exp, _ := jsonInt64(tok, "expires_at")
	if exp != 1120 {
		t.Fatalf("expires_at = %d, want 1120", exp)
	}
}

func TestSetJSONNullExpiresInFallsBackToDefault(t *testing.T) {
	tok := map[string]any{"expires_in": nil}
	g := NewGalaxyConfig()
	g.setJSONAt(tok, 1000)
	exp, _ := jsonInt64(tok, "expires_at")
	if exp != 1000+3600 {
		t.Fatalf("expires_at = %d, want %d", exp, 1000+3600)
	}
}

func TestSetJSONKeepsExistingExpiresAt(t *testing.T) {
	tok := map[string]any{"expires_at": float64(424242)}
	g := NewGalaxyConfig()
	g.setJSONAt(tok, 1000)
	exp, _ := jsonInt64(tok, "expires_at")
	if exp != 424242 {
		t.Fatalf("expires_at = %d, want 424242", exp)
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
	// Mirrors C++: an existing (even empty) member is returned as-is.
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
			})
		}
	}()
	wg.Wait()
	if g.GetAccessToken() != "tok-new" {
		t.Errorf("final access token = %q, want tok-new", g.GetAccessToken())
	}
}
