package config

import "testing"

// TestNewConfigPaths locks the config-domain defaults NewConfig derives from
// the two roots: the cache tree and the identity the config carries. Moved here
// from internal/cli, where it was asserting this package's constructor from the
// wrong layer.
func TestNewConfigPaths(t *testing.T) {
	cfg := NewConfig("/cfg", "/cache")
	if cfg.CacheDirectory != "/cache/goggo" || cfg.XMLDirectory != "/cache/goggo/xml" {
		t.Errorf("cache paths = %q / %q", cfg.CacheDirectory, cfg.XMLDirectory)
	}
	// The authentication files this build reads and writes. The cookie file's
	// name is not the one an earlier build used, and that file is never read.
	if cfg.Curl.CookiePath != "/cfg/goggo/cookies.bin" {
		t.Errorf("cookie path = %q, want /cfg/goggo/cookies.bin", cfg.Curl.CookiePath)
	}
	if cfg.VersionString != VersionString {
		t.Errorf("version = %q", cfg.VersionString)
	}
}
