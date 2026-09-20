package auth

import (
	"encoding/json"
	"errors"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/nekrozis/goggo/internal/config"
)

// newTestStore returns an empty, unbound store: the state a fresh install has
// before a location is chosen for it.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open("")
	if err != nil {
		t.Fatalf("Open(\"\"): %v", err)
	}
	return s
}

// newBoundStore returns an empty store bound to a file in a temporary
// directory, plus that path.
func newBoundStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tokens.json")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open(%q): %v", path, err)
	}
	return s, path
}

// tokenResponse is a token endpoint response in the shape the server sends it.
func tokenResponse(extra ...map[string]any) map[string]any {
	m := map[string]any{
		"access_token":  "at-1",
		"refresh_token": "rt-1",
		"expires_in":    3600,
		"user_id":       "u7",
	}
	for _, e := range extra {
		for k, v := range e {
			m[k] = v
		}
	}
	return m
}

// readStoredFile reads the token file back as the JSON tree it is. The surface
// hands out no copy of the store, so a test that needs to see what was written
// reads the file the store owns.
func readStoredFile(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %q: %v", path, err)
	}
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		t.Fatalf("decode %q: %v", path, err)
	}
	return obj
}

// TestOpenWithoutAPathIsAnEmptyStore: a store may exist before a location is
// chosen for it, and nothing about that is an error.
func TestOpenWithoutAPathIsAnEmptyStore(t *testing.T) {
	s, err := Open("")
	if err != nil {
		t.Fatalf("Open(\"\"): %v", err)
	}
	if !s.Empty() {
		t.Error("Empty() = false, want an empty store")
	}
	if !s.Expired() {
		t.Error("Expired() = false, want an empty store to hold no usable token")
	}
	if got := s.AuthorizationValue(); got != "" {
		t.Errorf("AuthorizationValue() = %q, want empty", got)
	}
	// Nothing can be persisted without a location, which is what makes the
	// empty path safe to hand out.
	s.StoreLoginResponse(tokenResponse())
	if err := s.Save(); err == nil {
		t.Error("Save() on an unbound store = nil, want an error")
	}
}

// TestOpenMissingFileIsAnEmptyStore: a fresh install is normal, not an error.
func TestOpenMissingFileIsAnEmptyStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.json")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open(missing): %v", err)
	}
	if !s.Empty() {
		t.Error("Empty() = false, want an empty store for a missing file")
	}
	if !s.Expired() {
		t.Error("Expired() = false, want no usable token")
	}
}

// TestOpenRejectsAnUnusableFile: a file that exists but is not a JSON object is
// an error, never a silently empty store.
func TestOpenRejectsAnUnusableFile(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"truncated", `{"access_token":`},
		{"array", `["a","b"]`},
		{"scalar", `"at"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "bad.json")
			if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Open(path); err == nil {
				t.Errorf("Open(%s file) = nil, want an error", tc.name)
			}
		})
	}
}

// TestAuthorizationValueHidesAnUnusableToken: the header value is handed out
// only when a request can actually use it.
func TestAuthorizationValueHidesAnUnusableToken(t *testing.T) {
	cases := []struct {
		name  string
		token map[string]any
		want  string
	}{
		{"fresh token", map[string]any{"access_token": "at", "expires_in": 3600}, "Bearer at"},
		{"expired token", map[string]any{"access_token": "at", "expires_in": -10}, ""},
		{"no access token", map[string]any{"refresh_token": "rt", "expires_in": 3600}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newTestStore(t)
			s.StoreLoginResponse(c.token)
			if got := s.AuthorizationValue(); got != c.want {
				t.Errorf("AuthorizationValue() = %q, want %q", got, c.want)
			}
		})
	}
	if got := newTestStore(t).AuthorizationValue(); got != "" {
		t.Errorf("AuthorizationValue() on an empty store = %q, want empty", got)
	}
}

// TestExpiredBoundary pins the comparison itself: a token is expired once the
// clock has passed expires_at, and not at the instant it equals it. The clock is
// injected because the rule is a comparison rather than an event.
func TestExpiredBoundary(t *testing.T) {
	s := newTestStore(t)
	s.StoreLoginResponse(map[string]any{"access_token": "at", "expires_at": float64(1000)})

	if s.expiredAtLocked(999) {
		t.Error("expiredAtLocked(999) = true, want false (now < expires_at)")
	}
	if s.expiredAtLocked(1000) {
		t.Error("expiredAtLocked(1000) = true, want false (now == expires_at)")
	}
	if !s.expiredAtLocked(1001) {
		t.Error("expiredAtLocked(1001) = false, want true (now > expires_at)")
	}
}

// TestExpiredWithoutExpiresAt: a token tree that carries no expires_at has no
// evidence of validity, so it counts as expired.
func TestExpiredWithoutExpiresAt(t *testing.T) {
	s := &Store{token: map[string]any{"access_token": "at"}}
	if !s.Expired() {
		t.Error("Expired() = false, want true: there is no expires_at to compare against")
	}
}

// TestUnusableExpiresAtCountsAsExpired: a value that cannot be read as a number
// cannot show the token to be valid either.
func TestUnusableExpiresAtCountsAsExpired(t *testing.T) {
	for _, bad := range []any{nil, "not-a-number", math.NaN(), math.Inf(1), math.Ldexp(1, 64)} {
		s := newTestStore(t)
		s.StoreLoginResponse(map[string]any{"access_token": "at", "expires_at": bad})
		if !s.Expired() {
			t.Errorf("expires_at %v: Expired() = false, want true", bad)
		}
	}
}

// TestStoreLoginResponseDerivesExpiresAt: the response carries a lifetime and the
// store turns it into the absolute expires_at the expiry check reads. The value
// itself is pinned through the file, because the surface deliberately has no
// getter for it.
func TestStoreLoginResponseDerivesExpiresAt(t *testing.T) {
	cases := []struct {
		name     string
		token    map[string]any
		lifetime int64
	}{
		{"the response's lifetime", map[string]any{"access_token": "at", "expires_in": float64(60)}, 60},
		{"no lifetime uses the default", map[string]any{"access_token": "at"}, defaultExpiresIn},
		{"a null lifetime uses the default", map[string]any{"access_token": "at", "expires_in": nil}, defaultExpiresIn},
		{"an unusable lifetime uses the default", map[string]any{"access_token": "at", "expires_in": "soon"}, defaultExpiresIn},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, path := newBoundStore(t)
			before := time.Now().Unix()
			s.StoreLoginResponse(c.token)
			if err := s.Save(); err != nil {
				t.Fatalf("Save: %v", err)
			}
			got, ok := jsonInt64(readStoredFile(t, path), "expires_at")
			if !ok {
				t.Fatal("stored expires_at is missing or not a number")
			}
			if got < before+c.lifetime || got > time.Now().Unix()+c.lifetime {
				t.Errorf("expires_at = %d, want now + %d", got, c.lifetime)
			}
		})
	}
}

// TestStoreLoginResponseKeepsTheResponseExpiresAt: an absolute expiry in the
// response wins over the lifetime.
func TestStoreLoginResponseKeepsTheResponseExpiresAt(t *testing.T) {
	s, path := newBoundStore(t)
	s.StoreLoginResponse(map[string]any{"access_token": "at", "expires_at": float64(424242)})
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if got, ok := jsonInt64(readStoredFile(t, path), "expires_at"); !ok || got != 424242 {
		t.Errorf("expires_at = %d (ok = %v), want the response's 424242", got, ok)
	}
}

// TestStoreLoginResponseNeverMutatesItsArgument locks both directions of the
// copy: the caller's map does not become the store's, and the store does not
// become the caller's.
func TestStoreLoginResponseNeverMutatesItsArgument(t *testing.T) {
	s, path := newBoundStore(t)
	token := map[string]any{
		"access_token": "at",
		"nested":       map[string]any{"list": []any{"a", "b"}},
	}
	s.StoreLoginResponse(token)

	// The derivation stays private: the caller's map never learns about it.
	if _, exists := token["expires_at"]; exists {
		t.Errorf("StoreLoginResponse added expires_at to the caller's map: %v", token)
	}

	// Mutating the caller's containers does not reach the store.
	nested := token["nested"].(map[string]any)
	nested["list"].([]any)[0] = "mutated"
	nested["extra"] = true
	token["access_token"] = "mutated"

	if got := s.AuthorizationValue(); got != "Bearer at" {
		t.Errorf("AuthorizationValue() = %q, want the stored token untouched", got)
	}
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	want := map[string]any{"list": []any{"a", "b"}}
	if got := readStoredFile(t, path)["nested"]; !reflect.DeepEqual(got, want) {
		t.Errorf("stored nested = %v, want %v", got, want)
	}
}

// TestStoreLoginResponseAcceptsNumericExpiresAt: the absolute expiry is read from
// whatever numeric shape the caller holds, an int64 included.
func TestStoreLoginResponseAcceptsNumericExpiresAt(t *testing.T) {
	s := newTestStore(t)
	s.StoreLoginResponse(map[string]any{"access_token": "at", "expires_at": int64(1)})
	if !s.Expired() {
		t.Error("Expired() = false, want true: expires_at 1 is in the past")
	}
	s.StoreLoginResponse(map[string]any{"access_token": "at", "expires_at": int64(1 << 40)})
	if s.Expired() {
		t.Error("Expired() = true, want false: expires_at 2^40 is far in the future")
	}
}

// TestClientIdentityFallsBackToTheProtocolConstants: the client identity is the
// same for every user until a stored override replaces it.
func TestClientIdentityFallsBackToTheProtocolConstants(t *testing.T) {
	s := newTestStore(t)
	if got := s.ClientID(); got != config.DefaultClientID {
		t.Errorf("ClientID() = %q, want the protocol default", got)
	}
	if got := s.ClientSecret(); got != config.DefaultClientSecret {
		t.Errorf("ClientSecret() = %q, want the protocol default", got)
	}
	if got := s.RedirectURI(); got != config.DefaultRedirectURI {
		t.Errorf("RedirectURI() = %q, want the protocol default", got)
	}

	s.StoreLoginResponse(map[string]any{"client_id": "my-client", "client_secret": "my-secret"})
	if got := s.ClientID(); got != "my-client" {
		t.Errorf("ClientID() = %q, want the stored override", got)
	}
	if got := s.ClientSecret(); got != "my-secret" {
		t.Errorf("ClientSecret() = %q, want the stored override", got)
	}
}

// TestEmptyClientOverrideIsReturnedVerbatim: the fallback is for an absent key,
// not for a blank one, so an existing empty override is the value.
func TestEmptyClientOverrideIsReturnedVerbatim(t *testing.T) {
	s := newTestStore(t)
	s.StoreLoginResponse(map[string]any{"client_id": "", "client_secret": ""})
	if got := s.ClientID(); got != "" {
		t.Errorf("ClientID() = %q, want the empty override", got)
	}
	if got := s.ClientSecret(); got != "" {
		t.Errorf("ClientSecret() = %q, want the empty override", got)
	}
}

// TestResetClientRestoresTheDefaults locks what ResetClient is for: a login
// starts from the protocol identity, never from whatever the last stored
// response left behind.
func TestResetClientRestoresTheDefaults(t *testing.T) {
	s := newTestStore(t)
	s.StoreLoginResponse(map[string]any{"client_id": "", "client_secret": ""})
	s.ResetClient()
	if got := s.ClientID(); got != config.DefaultClientID {
		t.Errorf("ClientID() after ResetClient = %q, want the protocol default", got)
	}
	if got := s.ClientSecret(); got != config.DefaultClientSecret {
		t.Errorf("ClientSecret() after ResetClient = %q, want the protocol default", got)
	}
}

// TestEmptyFollowsTheStoreContents: "empty" means no token at all, and it stops
// being true the moment a response is stored.
func TestEmptyFollowsTheStoreContents(t *testing.T) {
	s := newTestStore(t)
	if !s.Empty() {
		t.Error("Empty() = false on a fresh store")
	}
	s.StoreLoginResponse(map[string]any{"expires_in": 3600})
	if s.Empty() {
		t.Error("Empty() = true after a response was stored")
	}
}

// TestSaveOpenRoundTrip: what Save writes is what Open reads, every member of the
// response included.
func TestSaveOpenRoundTrip(t *testing.T) {
	s, path := newBoundStore(t)
	token := tokenResponse()
	s.StoreLoginResponse(token)
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if reopened.Empty() {
		t.Error("Empty() = true after a round trip")
	}
	if reopened.Expired() {
		t.Error("Expired() = true, want the stored lifetime to keep the token valid")
	}
	if got := reopened.AuthorizationValue(); got != "Bearer at-1" {
		t.Errorf("AuthorizationValue() = %q, want the stored token in header form", got)
	}

	// Values pass through JSON encoding on save and decoding on load, so compare
	// by JSON text rather than by Go type.
	stored := readStoredFile(t, path)
	for k, want := range token {
		wantJSON, err := json.Marshal(want)
		if err != nil {
			t.Fatal(err)
		}
		gotJSON, err := json.Marshal(stored[k])
		if err != nil {
			t.Fatal(err)
		}
		if string(gotJSON) != string(wantJSON) {
			t.Errorf("field %q = %s, want %s", k, gotJSON, wantJSON)
		}
	}
}

// TestOpenDerivesMissingExpiresAtFromTheFileMtime: a file written before
// expires_at was recorded keeps the lifetime it was written with — the file's
// modification time plus expires_in — rather than starting a fresh one at the
// moment it is read.
func TestOpenDerivesMissingExpiresAtFromTheFileMtime(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		lifetime int64
	}{
		{"with a lifetime", `{"access_token":"at-old","refresh_token":"rt-old","expires_in":3600}`, 3600},
		{"without a lifetime", `{"access_token":"at-old","refresh_token":"rt-old"}`, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "legacy.json")
			if err := os.WriteFile(path, []byte(c.body), 0o600); err != nil {
				t.Fatal(err)
			}
			mt := time.Date(2020, 4, 15, 12, 30, 0, 0, time.Local)
			if err := os.Chtimes(path, mt, mt); err != nil {
				t.Fatal(err)
			}

			s, err := Open(path)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			// The mtime is in the past, so an expiry derived from the file is
			// already past while one derived from "now" would not be.
			if !s.Expired() {
				t.Error("Expired() = false, want true: the expiry comes from the file, not from the read")
			}
			if err := s.Save(); err != nil {
				t.Fatalf("Save: %v", err)
			}
			got, ok := jsonInt64(readStoredFile(t, path), "expires_at")
			if !ok {
				t.Fatal("stored expires_at is missing or not a number")
			}
			if want := mt.Unix() + c.lifetime; got != want {
				t.Errorf("expires_at = %d, want %d (the file's mtime plus expires_in)", got, want)
			}
		})
	}
}

// TestSaveEmptyStoreWritesNothing: a file holding no credential is a file that
// can only confuse the next read.
func TestSaveEmptyStoreWritesNothing(t *testing.T) {
	s, path := newBoundStore(t)
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("file exists after an empty save, stat err = %v", err)
	}
}

// TestSaveReplacesTheStoredToken: a second save overwrites the first.
func TestSaveReplacesTheStoredToken(t *testing.T) {
	s, path := newBoundStore(t)
	s.StoreLoginResponse(tokenResponse())
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	s.StoreLoginResponse(tokenResponse(map[string]any{"access_token": "at-2", "refresh_token": "rt-2"}))
	if err := s.Save(); err != nil {
		t.Fatalf("second Save: %v", err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got := reopened.AuthorizationValue(); got != "Bearer at-2" {
		t.Errorf("AuthorizationValue() = %q, want at-2", got)
	}
}

// TestSavedFileModeIs0600 verifies the atomic write lands at 0600 and leaves no
// temp file behind. Unix-only: Windows permission semantics differ.
func TestSavedFileModeIs0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission mode assertions are Unix-only")
	}
	s, path := newBoundStore(t)
	s.StoreLoginResponse(tokenResponse())
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Errorf("mode = %o, want 0600", got)
	}
	leftovers, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".goggo-token-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(leftovers) != 0 {
		t.Errorf("temp files left behind: %v", leftovers)
	}
}

// TestRemoveStoreIsIdempotent: logout clears state that may not be there, and a
// path-level removal must not need the file to parse first.
func TestRemoveStoreIsIdempotent(t *testing.T) {
	s, path := newBoundStore(t)
	s.StoreLoginResponse(tokenResponse())
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := RemoveStore(path); err != nil {
		t.Fatalf("RemoveStore: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("store still present after RemoveStore: %v", err)
	}
	if err := RemoveStore(path); err != nil {
		t.Errorf("second RemoveStore = %v, want nil for an already-gone store", err)
	}
	// A corrupt file is removable too: nothing is parsed on this path.
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RemoveStore(path); err != nil {
		t.Errorf("RemoveStore on a corrupt file = %v, want nil", err)
	}
}

// TestStoreConcurrentAccess: the store is shared by pointer, so its readers and
// its writer must not race.
func TestStoreConcurrentAccess(t *testing.T) {
	s := newTestStore(t)
	s.StoreLoginResponse(tokenResponse())

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				s.Expired()
				s.AuthorizationValue()
				s.ClientID()
				s.ClientSecret()
				s.RedirectURI()
				s.Empty()
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 200; j++ {
			s.StoreLoginResponse(map[string]any{
				"access_token": "tok-new",
				"expires_in":   float64(3600),
				"client_id":    "custom",
			})
			s.ResetClient()
		}
	}()
	wg.Wait()

	if got := s.AuthorizationValue(); got != "Bearer tok-new" {
		t.Errorf("final AuthorizationValue() = %q, want the last stored token", got)
	}
}
