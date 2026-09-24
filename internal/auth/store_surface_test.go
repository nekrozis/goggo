package auth

import (
	"reflect"
	"strings"
	"testing"
)

// storeSurface is the frozen export surface of the credential seam. It is the
// contract, not a summary: a method that is not on this list is a way for a
// credential to leave the seam without anyone deciding that it should.
var storeSurface = map[string]bool{
	// Use: the value a request needs, never the token on its own.
	"AuthorizationValue": true,
	"Expired":            true,
	"Empty":              true,

	// Protocol identity: the same for every user, not derived from an account.
	"ClientID":     true,
	"ClientSecret": true,
	"RedirectURI":  true,
	"ResetClient":  true,

	// Lifecycle: write, renew, persist.
	"StoreLoginResponse": true,
	"Refresh":            true,
	"Save":               true,
}

// forbiddenNames are the accessors the seam must never grow back. They are
// listed explicitly because the failure mode is subtle: GetAccessToken looks
// harmless at the call site and is a leak waiting for a caller to print it.
var forbiddenNames = []string{
	"GetJSON", "GetAccessToken", "GetRefreshToken", "GetUserID",
	"Secret", "Token", "Raw", "RawToken", "String", "Format", "MarshalJSON",
}

// TestStoreExportSurfaceIsFrozen locks the surface in both directions: nothing
// outside the frozen list may exist, and the count must match exactly so a
// rename cannot slip through as "a new method that is not on the forbidden
// list".
func TestStoreExportSurfaceIsFrozen(t *testing.T) {
	typ := reflect.TypeFor[*Store]()

	for method := range typ.Methods() {
		name := method.Name
		if !storeSurface[name] {
			t.Errorf("auth.Store exposes %s, which is not on the frozen surface %v", name, surfaceNames())
		}
	}
	if got := typ.NumMethod(); got != len(storeSurface) {
		t.Errorf("auth.Store has %d exported methods, want exactly %d: %v", got, len(storeSurface), surfaceNames())
	}
	for _, banned := range forbiddenNames {
		if _, ok := typ.MethodByName(banned); ok {
			t.Errorf("auth.Store must not expose %s: a generic raw-secret accessor is what this seam exists to prevent", banned)
		}
	}
}

// TestStoreCarriesNoRawSecretThroughItsSurface is the property the surface
// exists for, stated as behaviour rather than as a name list: the only value the
// seam hands out is the one a request sends.
func TestStoreCarriesNoRawSecretThroughItsSurface(t *testing.T) {
	const access, refresh = "ACCESS-SENTINEL-4c1f", "REFRESH-SENTINEL-9a2e"

	s := &Store{token: map[string]any{
		"access_token":  access,
		"refresh_token": refresh,
		"expires_at":    float64(1 << 40),
	}}

	authz := s.AuthorizationValue()
	if !strings.Contains(authz, access) {
		t.Fatalf("AuthorizationValue() = %q, want the access token in its header form", authz)
	}
	if !strings.HasPrefix(authz, "Bearer ") {
		t.Errorf("AuthorizationValue() = %q, want the Bearer prefix: the seam hands out the value a request needs, not the token", authz)
	}
	// The refresh token has no reason to appear anywhere on the surface.
	if strings.Contains(authz, refresh) {
		t.Errorf("AuthorizationValue() = %q, want no refresh token", authz)
	}
	for _, name := range []string{"ClientID", "ClientSecret", "RedirectURI"} {
		got := reflect.ValueOf(s).MethodByName(name).Call(nil)[0].String()
		if strings.Contains(got, refresh) {
			t.Errorf("%s() = %q, want no refresh token", name, got)
		}
	}
}

// surfaceNames returns the frozen surface sorted, for readable failures.
func surfaceNames() []string {
	names := make([]string, 0, len(storeSurface))
	for n := range storeSurface {
		names = append(names, n)
	}
	return names
}
