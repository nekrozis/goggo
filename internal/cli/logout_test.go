package cli

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/nekrozis/goggo/internal/config"
	"github.com/nekrozis/goggo/internal/core"
)

// newLogoutConfig builds a configuration rooted in a temporary tree, laid out
// the way the real one is: the authentication files and the configuration file
// share the configuration directory, while the cache (with its xml
// subdirectory) lives under a separate root.
//
// Both roots are distinct on purpose — with a single root the cache and the
// configuration directory would be the same directory and the "cache survives
// --logout" assertion below would be vacuous.
func newLogoutConfig(t *testing.T) config.Config {
	t.Helper()
	root := t.TempDir()
	cfg := config.NewConfig(filepath.Join(root, "config"), filepath.Join(root, "cache"))
	for _, dir := range []string{cfg.ConfigDirectory, cfg.XMLDirectory} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return cfg
}

func writeTestFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

// invalidPath is a path the OS never accepts. It is how a removal failure is
// induced deterministically: the obvious refusals (a non-empty directory,
// permissions, a locked file) cannot be used in this test suite, because the
// sandbox redirects deletions and reports them as successes even for a
// non-empty directory. A test built on one of those would assert the
// environment, not the code.
const invalidPath = "invalid\x00path"

// logoutClearedLine is the one success line the logout path prints. It is
// pinned here once — the wording is user-readable, so the tests that only care
// that the run reported success share this literal instead of restating it.
const logoutClearedLine = "Local login state cleared\n"

// TestLogoutClearsAuthenticationStateOnly locks the scope of --logout: the two
// authentication files go and everything sharing the tree stays — a future
// "helpful" widening of the deletion set fails here.
func TestLogoutClearsAuthenticationStateOnly(t *testing.T) {
	cfg := newLogoutConfig(t)

	authFiles := []string{core.TokenPath(cfg), cfg.Curl.CookiePath}
	for _, p := range authFiles {
		writeTestFile(t, p, "auth state")
	}
	// Everything that is not authentication state, including the cache root and
	// the XML file inside it.
	kept := []string{
		cfg.ConfigFilePath,
		cfg.BlacklistFilePath,
		cfg.IgnorelistFilePath,
		cfg.TransformConfigFilePath,
		filepath.Join(cfg.XMLDirectory, "game.xml"),
	}
	for _, p := range kept {
		writeTestFile(t, p, "keep me")
	}

	var out bytes.Buffer
	if err := logout(cfg, &out); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if got := out.String(); got != logoutClearedLine {
		t.Errorf("stdout = %q, want %q", got, logoutClearedLine)
	}
	for _, p := range authFiles {
		if _, err := os.Stat(p); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s must be removed, stat err = %v", p, err)
		}
	}
	for _, p := range kept {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s must survive --logout: %v", p, err)
		}
	}
}

// TestLogoutIsIdempotent locks the idempotence rule: logging out of a session
// that is already gone is a success, not an error, so the operation can be
// repeated. This is the rule's home — removeAuthFile's own "absent is already
// logged out" branch is the implementation of it.
func TestLogoutIsIdempotent(t *testing.T) {
	cfg := newLogoutConfig(t)
	writeTestFile(t, core.TokenPath(cfg), "{}")
	writeTestFile(t, cfg.Curl.CookiePath, "cookies")

	for i := 1; i <= 3; i++ {
		var out bytes.Buffer
		if err := logout(cfg, &out); err != nil {
			t.Fatalf("logout #%d: %v", i, err)
		}
		if got := out.String(); got != logoutClearedLine {
			t.Errorf("logout #%d stdout = %q, want %q", i, got, logoutClearedLine)
		}
	}
}

// TestLogoutOnMissingDirectoryIsSuccess covers the fresh-install case: there is
// nothing to remove, and the run must not create the directory just to delete
// from it.
func TestLogoutOnMissingDirectoryIsSuccess(t *testing.T) {
	root := t.TempDir()
	cfg := config.NewConfig(filepath.Join(root, "absent"), filepath.Join(root, "absent"))

	var out bytes.Buffer
	if err := logout(cfg, &out); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if got := out.String(); got != logoutClearedLine {
		t.Errorf("stdout = %q, want %q", got, logoutClearedLine)
	}
	if _, err := os.Stat(cfg.ConfigDirectory); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("logout must not create %s (stat err = %v)", cfg.ConfigDirectory, err)
	}
}

// TestLogoutPropagatesRemovalFailure locks the error face: a removal that is
// neither a success nor "already gone" stops the run and prints no success
// line, so the output never claims more than happened.
//
// It also pins the non-transactional behaviour: the token file is removed
// before the cookie path is attempted, so it is already gone when the second
// removal fails. The caller learns about the failure from the error, not from
// the resulting state.
func TestLogoutPropagatesRemovalFailure(t *testing.T) {
	cfg := newLogoutConfig(t)
	writeTestFile(t, core.TokenPath(cfg), "{}")
	cfg.Curl.CookiePath = invalidPath

	var out bytes.Buffer
	err := logout(cfg, &out)
	if err == nil {
		t.Fatal("logout must report a removal failure")
	}
	if out.Len() != 0 {
		t.Errorf("stdout = %q, want no success line when the run failed", out.String())
	}
	if _, statErr := os.Stat(core.TokenPath(cfg)); !errors.Is(statErr, fs.ErrNotExist) {
		t.Errorf("token file must already be gone (stat err = %v)", statErr)
	}
}

// TestRemoveAuthFileReportsOtherFailures locks the classification: a failure
// that is not fs.ErrNotExist becomes an error that still names the path.
//
// The path is checked through errors.As rather than by matching the rendered
// message, so the assertion does not depend on how the OS words the error. The
// other branch — an absent path is already logged out — is the idempotence rule
// locked by TestLogoutIsIdempotent, at the level a user can see it.
func TestRemoveAuthFileReportsOtherFailures(t *testing.T) {
	err := removeAuthFile(invalidPath)
	if err == nil {
		t.Fatal("removeAuthFile must report a failure it did not classify as already gone")
	}
	if errors.Is(err, fs.ErrNotExist) {
		t.Errorf("err = %v, must not be classified as already gone", err)
	}
	var pathErr *fs.PathError
	if !errors.As(err, &pathErr) {
		t.Fatalf("err = %v, want it to wrap the underlying *fs.PathError", err)
	}
	if pathErr.Path != invalidPath {
		t.Errorf("PathError.Path = %q, want %q", pathErr.Path, invalidPath)
	}
}
