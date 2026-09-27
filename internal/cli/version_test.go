package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nekrozis/goggo/internal/config"
)

// TestRenderVersionDevelopment locks the default surface: a build made without
// -ldflags prints one line, the program's own version, and nothing else. A
// script parsing --version relies on this shape.
func TestRenderVersionDevelopment(t *testing.T) {
	if config.BuildCommit != "" {
		t.Skip("a stamped build does not exercise the development shape")
	}
	var b bytes.Buffer
	renderVersion(&b)
	want := config.VersionString + "\n"
	if got := b.String(); got != want {
		t.Errorf("renderVersion = %q, want %q", got, want)
	}
}

// TestRenderVersionStamped locks the release shape: when a build stamps the
// commit, --version names it on the same line, and the compatibility baseline
// is never reintroduced.
func TestRenderVersionStamped(t *testing.T) {
	old := config.BuildCommit
	t.Cleanup(func() { config.BuildCommit = old })
	config.BuildCommit = "deadbeef"

	var b bytes.Buffer
	renderVersion(&b)
	got := b.String()
	if !strings.HasPrefix(got, config.VersionString+" (commit deadbeef)\n") {
		t.Errorf("renderVersion = %q, want the version line with the commit", got)
	}
	if strings.Contains(got, "compatibility") {
		t.Errorf("renderVersion = %q must not print a compatibility baseline", got)
	}
}
