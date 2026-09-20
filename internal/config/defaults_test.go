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
	if cfg.VersionString != VersionString {
		t.Errorf("version = %q", cfg.VersionString)
	}
}
