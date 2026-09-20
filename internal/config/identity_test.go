package config

import (
	"strings"
	"testing"
)

// TestIdentityIsSeparateFromCompatibility locks the three-layer identity: the
// program presents itself, and names the release it tracks only as a
// compatibility baseline.
//
// This is a contract pin, not a value echo: VersionString is what the CLI
// prints and DefaultUserAgent is what the servers see, so another layer reads
// both. Moved here from internal/cli, where it was asserting this package's
// constants from the wrong layer.
func TestIdentityIsSeparateFromCompatibility(t *testing.T) {
	if Version == UpstreamCompatibilityVersion {
		t.Fatalf("own version %q must not equal the compatibility baseline", Version)
	}
	if !strings.HasPrefix(VersionString, ProgramName+" ") {
		t.Errorf("VersionString = %q, want it to start with the program name", VersionString)
	}
	if strings.Contains(VersionString, UpstreamName) {
		t.Errorf("VersionString = %q must not present another project as our identity", VersionString)
	}

	ua := DefaultUserAgent()
	if !strings.HasPrefix(ua, ProgramName+"/"+Version) {
		t.Errorf("UserAgent = %q, want it to start with %q", ua, ProgramName+"/"+Version)
	}
	if strings.Contains(ua, UpstreamName) {
		t.Errorf("UserAgent = %q must not carry another product's name", ua)
	}
}
