package config

import (
	"strings"
	"testing"
)

// TestIdentityIsOurOwn locks the identity surfaces: VersionString is what the
// CLI prints and DefaultUserAgent is what the servers see, so both are pinned
// here through the values another layer reads rather than echoed as literals.
// The program presents itself alone: it never carries another project's name
// into either surface.
func TestIdentityIsOurOwn(t *testing.T) {
	if !strings.HasPrefix(VersionString, ProgramName+" ") {
		t.Errorf("VersionString = %q, want it to start with the program name", VersionString)
	}
	if strings.Contains(VersionString, "LGOGDownloader") {
		t.Errorf("VersionString = %q must not present another project as our identity", VersionString)
	}

	ua := DefaultUserAgent()
	if !strings.HasPrefix(ua, ProgramName+"/"+Version) {
		t.Errorf("UserAgent = %q, want it to start with %q", ua, ProgramName+"/"+Version)
	}
	if strings.Contains(ua, "LGOGDownloader") {
		t.Errorf("UserAgent = %q must not carry another product's name", ua)
	}
}
