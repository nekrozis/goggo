package util

import (
	"fmt"
	"os"
	"path/filepath"
)

// HomeDir mirrors Util::getHomeDir (util.cpp:460-470). The C++ code prints
// an error and exits when $HOME is missing; Go returns an error instead
// (intentional difference: keep the failure, drop the process exit).
func HomeDir() (string, error) {
	home, ok := os.LookupEnv("HOME")
	if !ok {
		return "", fmt.Errorf("HOME environment variable is not set")
	}
	return home, nil
}

// ConfigHome mirrors Util::getConfigHome (util.cpp:472-481). os.LookupEnv is
// used so that an XDG_CONFIG_HOME that exists but is empty returns "" exactly
// like C++ (getenv returns a non-NULL empty string there); only a missing
// variable falls back to $HOME/.config.
func ConfigHome() (string, error) {
	if v, ok := os.LookupEnv("XDG_CONFIG_HOME"); ok {
		return v, nil
	}
	home, err := HomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config"), nil
}

// CacheHome mirrors Util::getCacheHome (util.cpp:483-492); same
// empty-vs-missing semantics as ConfigHome.
func CacheHome() (string, error) {
	if v, ok := os.LookupEnv("XDG_CACHE_HOME"); ok {
		return v, nil
	}
	home, err := HomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".cache"), nil
}
