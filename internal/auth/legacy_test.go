package auth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nekrozis/goggo/internal/config"
)

// legacyStoreName is the file an earlier build wrote: plaintext JSON at the
// configuration root. Nothing reads it any more, and this round exists to make
// that true rather than merely intended — so the name appears only in this file.
// A change that reintroduced it would have to delete these tests first.
const legacyStoreName = "galaxy_tokens.json"

// legacyCfg is a configuration whose directory is a temporary one, so the store
// path and the legacy path sit beside each other with nothing else around.
func legacyCfg(t *testing.T) config.Config {
	t.Helper()
	return config.Config{ConfigDirectory: t.TempDir()}
}

// writeLegacy plants a legacy token file, in the shape the earlier build wrote:
// plaintext JSON.
func writeLegacy(t *testing.T, cfg config.Config, body string) string {
	t.Helper()
	path := filepath.Join(cfg.ConfigDirectory, legacyStoreName)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// seedStore writes a usable store through the seam, so the fixture never has to
// know the on-disk format.
func seedStore(t *testing.T, cfg config.Config, accessToken string) {
	t.Helper()
	s, err := Open(StorePath(cfg))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	s.StoreLoginResponse(map[string]any{
		"access_token":  accessToken,
		"refresh_token": "refresh-" + accessToken,
		"expires_in":    3600,
	})
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
}

// TestLegacyStoreIsNeverRead is the round's central claim, in its four shapes.
// The third case is the one that would otherwise be invisible: a legacy file
// holding a perfectly valid token must still leave the session logged out,
// because the decision is "which file is read", not "is the token good".
func TestLegacyStoreIsNeverRead(t *testing.T) {
	const legacyToken = "legacy-access-token"

	t.Run("only the legacy file: no session", func(t *testing.T) {
		cfg := legacyCfg(t)
		writeLegacy(t, cfg, `{"access_token":"`+legacyToken+`","refresh_token":"r","expires_in":3600}`)

		s, err := Open(StorePath(cfg))
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		if !s.Empty() {
			t.Error("Empty() = false, want the legacy file to contribute nothing")
		}
		if got := s.AuthorizationValue(); got != "" {
			t.Errorf("AuthorizationValue() = %q, want empty", got)
		}
	})

	t.Run("both present: only the new store counts", func(t *testing.T) {
		cfg := legacyCfg(t)
		writeLegacy(t, cfg, `{"access_token":"`+legacyToken+`","refresh_token":"r","expires_in":3600}`)
		seedStore(t, cfg, "current-access-token")

		s, err := Open(StorePath(cfg))
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		got := s.AuthorizationValue()
		if strings.Contains(got, legacyToken) {
			t.Errorf("AuthorizationValue() = %q, want no fallback to the legacy file", got)
		}
		if !strings.Contains(got, "current-access-token") {
			t.Errorf("AuthorizationValue() = %q, want the new store's token", got)
		}
	})

	t.Run("a valid legacy token and no new store: still no session", func(t *testing.T) {
		cfg := legacyCfg(t)
		// The legacy file is deliberately well-formed and unexpired: if the
		// outcome were "logged out because the token is bad", this case would
		// prove nothing.
		writeLegacy(t, cfg, `{"access_token":"`+legacyToken+`","expires_at":99999999999}`)

		s, err := Open(StorePath(cfg))
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		if !s.Empty() || s.AuthorizationValue() != "" {
			t.Errorf("store = empty %v, header %q; want no session from a valid legacy token",
				s.Empty(), s.AuthorizationValue())
		}
	})

	t.Run("a corrupt legacy file does not disturb the new store", func(t *testing.T) {
		cfg := legacyCfg(t)
		writeLegacy(t, cfg, "{not json")
		seedStore(t, cfg, "current-access-token")

		s, err := Open(StorePath(cfg))
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		if !strings.Contains(s.AuthorizationValue(), "current-access-token") {
			t.Errorf("AuthorizationValue() = %q, want the new store used regardless of the legacy file",
				s.AuthorizationValue())
		}
	})
}

// TestRemoveStoreLeavesTheLegacyFileAlone: logout clears the session this build
// owns. The legacy file belongs to an earlier build and is not this round's to
// delete — nor to rewrite, which is why the bytes are compared rather than the
// existence.
func TestRemoveStoreLeavesTheLegacyFileAlone(t *testing.T) {
	cfg := legacyCfg(t)
	const legacyBody = `{"access_token":"legacy-access-token","refresh_token":"r"}`
	legacyPath := writeLegacy(t, cfg, legacyBody)
	seedStore(t, cfg, "current-access-token")

	if err := RemoveStore(StorePath(cfg)); err != nil {
		t.Fatalf("RemoveStore: %v", err)
	}
	if _, err := os.Stat(StorePath(cfg)); !os.IsNotExist(err) {
		t.Errorf("stat(%q) = %v, want the new store gone", StorePath(cfg), err)
	}

	after, err := os.ReadFile(legacyPath)
	if err != nil {
		t.Fatalf("the legacy file was removed: %v", err)
	}
	if string(after) != legacyBody {
		t.Errorf("legacy file = %q, want it byte-identical", after)
	}
}

// TestLegacyStoreWithASentinelIsNeverLoaded is the leak-shaped half of the same
// claim: a credential planted in the legacy file must not reach a header value,
// whatever else the machine holds.
func TestLegacyStoreWithASentinelIsNeverLoaded(t *testing.T) {
	const sentinel = "LEGACY-SENTINEL-7d2b"

	cfg := legacyCfg(t)
	writeLegacy(t, cfg, `{"access_token":"`+sentinel+`","refresh_token":"`+sentinel+`","expires_at":99999999999}`)

	s, err := Open(StorePath(cfg))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got := s.AuthorizationValue(); strings.Contains(got, sentinel) {
		t.Errorf("AuthorizationValue() = %q, want the legacy sentinel absent", got)
	}
}

// TestTheStoredFileCarriesNoPlaintextCredential locks what the envelope is for:
// the file must not be a text file holding the credential fields. It is not a
// claim about confidentiality — the obfuscation is reversible by anyone with
// this source — only about the file not being plain text.
func TestTheStoredFileCarriesNoPlaintextCredential(t *testing.T) {
	cfg := legacyCfg(t)
	const access, refresh = "ACCESS-SENTINEL-4c1f", "REFRESH-SENTINEL-9a2e"
	seedStore(t, cfg, access)

	data, err := os.ReadFile(StorePath(cfg))
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{access, refresh, "access_token", "refresh_token"} {
		if strings.Contains(string(data), needle) {
			t.Errorf("the stored file contains %q in the clear", needle)
		}
	}
	if !strings.HasPrefix(string(data), storeMagic) {
		t.Errorf("stored file starts with %q, want the magic", data[:min(8, len(data))])
	}
}
