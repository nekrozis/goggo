package core

import (
	"testing"

	"github.com/nekrozis/goggo/internal/config"
)

// installTestConfig builds the configuration Parse would produce, so these
// tests exercise the resolution rather than the parser.
func installTestConfig(t *testing.T) config.Config {
	t.Helper()
	dir := t.TempDir()
	cfg := config.NewConfig(dir, dir)
	cfg.Directories.GalaxyInstallSubdir = "%install_dir%"
	cfg.DownloadConfig.GalaxyPlatform = config.PlatformWindows
	cfg.DownloadConfig.GalaxyArch = config.ArchX64
	cfg.DownloadConfig.GalaxyDependencies = true
	return cfg
}

// TestNewInstallRequestResolvesValues locks that the request carries resolved
// values: the architecture is a flag, not the "x64" the user typed.
func TestNewInstallRequestResolvesValues(t *testing.T) {
	req := NewInstallRequest(installTestConfig(t), "1495134320", "2", ProductRefExact)

	if req.ProductID != "1495134320" || req.BuildID != "2" {
		t.Errorf("product/build = %q/%q, want 1495134320/2", req.ProductID, req.BuildID)
	}
	if req.Platform != "windows" {
		t.Errorf("platform = %q, want windows", req.Platform)
	}
	if req.SubdirTemplate != "%install_dir%" {
		t.Errorf("subdir template = %q, want the configured template", req.SubdirTemplate)
	}
	if req.Arch != config.ArchX64 {
		t.Errorf("arch = %d, want the 64-bit flag rather than a flag string", req.Arch)
	}
	if !req.IncludeDependencies {
		t.Error("IncludeDependencies = false, want true")
	}
}

// TestNewInstallRequestPlatform locks the manifest path segment the API is
// called with.
func TestNewInstallRequestPlatform(t *testing.T) {
	cases := []struct {
		name string
		mask uint32
		want string
	}{
		{name: "windows", mask: config.PlatformWindows, want: "windows"},
		{name: "mac", mask: config.PlatformMac, want: "osx"},
		{name: "linux", mask: config.PlatformLinux, want: "linux"},
		{name: "unset falls back to windows", mask: 0, want: "windows"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := installTestConfig(t)
			cfg.DownloadConfig.GalaxyPlatform = c.mask
			if got := NewInstallRequest(cfg, "1", "", ProductRefExact).Platform; got != c.want {
				t.Errorf("platform = %q, want %q", got, c.want)
			}
		})
	}
}

// TestNewInstallRequestLanguageCarriesTheRawValue locks that the request
// carries --language exactly as typed. Which languages the build offers is not
// known until its manifest is fetched, so the resolution belongs to the plan and
// the request must not pre-empt it — and the empty string must survive as the
// flag's absence, which is what selects the default request later.
func TestNewInstallRequestLanguageCarriesTheRawValue(t *testing.T) {
	if got := NewInstallRequest(installTestConfig(t), "1", "", ProductRefExact).Language; got != "" {
		t.Errorf("language = %q, want empty when the flag was absent", got)
	}

	for _, value := range []string{"zh-Hant", "en", "klingon"} {
		cfg := installTestConfig(t)
		cfg.DownloadConfig.GalaxyLanguageRaw = value
		if got := NewInstallRequest(cfg, "1", "", ProductRefExact).Language; got != value {
			t.Errorf("language = %q, want %q", got, value)
		}
	}
}

// TestNewInstallRequestDependencies locks the --galaxy-no-dependencies
// direction: the setting is the positive value.
func TestNewInstallRequestDependencies(t *testing.T) {
	cfg := installTestConfig(t)
	cfg.DownloadConfig.GalaxyDependencies = false
	if NewInstallRequest(cfg, "1", "", ProductRefExact).IncludeDependencies {
		t.Error("IncludeDependencies = true, want false when the setting is off")
	}
}
