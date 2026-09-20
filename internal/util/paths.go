package util

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// HomeDir returns $HOME. A missing HOME is an error rather than a process exit.
//
// HOME is only consulted by the XDG branch below, so a missing HOME does not
// block startup on the platforms that resolve their roots through the standard
// library.
func HomeDir() (string, error) {
	home, ok := os.LookupEnv("HOME")
	if !ok {
		return "", fmt.Errorf("HOME environment variable is not set")
	}
	return home, nil
}

// usesStdlibRoots reports whether the per-user roots follow the platform
// convention (standard library) instead of the XDG convention.
//
// Windows: %AppData% / %LocalAppData%. macOS: ~/Library/Application Support and
// ~/Library/Caches. Linux and the BSDs keep the XDG rules.
//
// os.UserConfigDir must NOT be used on Linux: it falls back to $HOME/.config
// when an XDG variable is set but empty, whereas an empty-but-set variable must
// yield "".
func usesStdlibRoots() bool {
	return runtime.GOOS == "windows" || runtime.GOOS == "darwin"
}

// ConfigHome returns the per-user configuration root.
//
// Platform split:
//
//   - Windows: %AppData% (roaming).
//   - macOS: ~/Library/Application Support.
//   - Linux/BSD: XDG_CONFIG_HOME when the variable is set — even when it is
//     empty, which yields "" — otherwise $HOME/.config; a missing HOME is an
//     error.
func ConfigHome() (string, error) {
	if usesStdlibRoots() {
		return os.UserConfigDir()
	}
	if v, ok := os.LookupEnv("XDG_CONFIG_HOME"); ok {
		return v, nil
	}
	home, err := HomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config"), nil
}

// CacheHome returns the per-user cache root; same platform split and the same
// empty-vs-missing semantics as ConfigHome.
func CacheHome() (string, error) {
	if usesStdlibRoots() {
		return os.UserCacheDir()
	}
	if v, ok := os.LookupEnv("XDG_CACHE_HOME"); ok {
		return v, nil
	}
	home, err := HomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".cache"), nil
}
