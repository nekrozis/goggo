package cli

import (
	"strings"
	"testing"

	"github.com/nekrozis/goggo/internal/config"
)

func testDefaults() config.Config {
	return config.NewConfig("/cfg", "/cache")
}

func parse(t *testing.T, args ...string) Invocation {
	t.Helper()
	inv, err := Parse(args, testDefaults())
	if err != nil {
		t.Fatalf("Parse(%v): %v", args, err)
	}
	return inv
}

func TestParseDefaultsComeFromConfig(t *testing.T) {
	inv := parse(t)
	if inv.Config.Directories.Directory != "." {
		t.Errorf("directory default = %q, want .", inv.Config.Directories.Directory)
	}
	if inv.Config.PlatformPriority != config.DefaultPlatformPriority {
		t.Errorf("platform default = %q", inv.Config.PlatformPriority)
	}
	if inv.Config.Retries != 3 || inv.Config.Wait != 0 {
		t.Errorf("retries/wait defaults = %d/%d", inv.Config.Retries, inv.Config.Wait)
	}
	if !inv.Config.Color {
		t.Error("colour must default to on")
	}
	if inv.Config.DownloadConfig.Include != config.IncludeAllMask() {
		t.Errorf("include default = %d, want the all mask", inv.Config.DownloadConfig.Include)
	}
	if inv.Config.Curl.CookiePath != "/cfg/goggo/cookies.txt" {
		t.Errorf("cookie path = %q", inv.Config.Curl.CookiePath)
	}
	if !strings.HasPrefix(inv.Config.Curl.UserAgent, config.ProgramName+"/"+config.Version) {
		t.Errorf("user agent = %q", inv.Config.Curl.UserAgent)
	}
}

// TestParseFlagsOverrideDefaults locks the defaults-then-flags precedence.
func TestParseFlagsOverrideDefaults(t *testing.T) {
	inv := parse(t, "--directory", "/games", "--retries", "7", "--wait", "5",
		"--no-color", "--respect-umask", "--include-hidden-products")
	if inv.Config.Directories.Directory != "/games" {
		t.Errorf("directory = %q", inv.Config.Directories.Directory)
	}
	if inv.Config.Retries != 7 || inv.Config.Wait != 5 {
		t.Errorf("retries/wait = %d/%d", inv.Config.Retries, inv.Config.Wait)
	}
	if inv.Config.Color {
		t.Error("--no-color must clear colour")
	}
	if !inv.Config.RespectUmask || !inv.Config.IncludeHiddenProducts {
		t.Errorf("flags = %+v", inv.Config)
	}
}

func TestParsePlatformAndLanguage(t *testing.T) {
	inv := parse(t, "--platform", "w+l", "--language", "en")
	if inv.Config.DownloadConfig.InstallerPlatform != config.PlatformWindows|config.PlatformLinux {
		t.Errorf("platform mask = %d", inv.Config.DownloadConfig.InstallerPlatform)
	}
	// "w+l" is one comma-group combining two platforms, so the priority list
	// holds a single entry carrying both bits.
	if got := inv.Config.DownloadConfig.PlatformPriority; len(got) != 1 ||
		got[0] != config.PlatformWindows|config.PlatformLinux {
		t.Errorf("platform priority = %v", got)
	}
	if inv.Config.DownloadConfig.InstallerLanguage != config.LangEN {
		t.Errorf("language mask = %d", inv.Config.DownloadConfig.InstallerLanguage)
	}

	if _, err := Parse([]string{"--platform", "nonsense"}, testDefaults()); err == nil {
		t.Error("invalid --platform must fail")
	}
	if _, err := Parse([]string{"--language", ""}, testDefaults()); err == nil {
		t.Error("empty --language must fail")
	}
}

func TestParseIncludeExclude(t *testing.T) {
	inv := parse(t, "--include", "all", "--exclude", "bi")
	want := config.IncludeAllMask() &^ config.GFBaseInstaller
	if inv.Config.DownloadConfig.Include != want {
		t.Errorf("include = %d, want %d", inv.Config.DownloadConfig.Include, want)
	}

	// --exclude alone keeps the "all" default on the include side.
	only := parse(t, "--exclude", "di")
	if only.Config.DownloadConfig.Include != config.IncludeAllMask()&^config.GFDLCInstaller {
		t.Errorf("include = %d", only.Config.DownloadConfig.Include)
	}
}

func TestParseListFormats(t *testing.T) {
	cases := []struct {
		args   []string
		format uint32
		unsup  string
	}{
		{[]string{"--list"}, config.ListFormatGames, ""},
		{[]string{"--list", "games"}, config.ListFormatGames, ""},
		{[]string{"--list", "tags"}, config.ListFormatTags, ""},
		{[]string{"--list", "wishlist"}, config.ListFormatWishlist, ""},
		// Known but unimplemented formats keep their parsed mask and are
		// reported as unsupported; an unknown format parses to 0.
		{[]string{"--list", "details"}, config.ListFormatDetailsText, "--list details"},
		{[]string{"--list", "json"}, config.ListFormatDetailsJSON, "--list json"},
		{[]string{"--list", "nonsense"}, 0, "--list nonsense"},
		{[]string{"--list=wishlist"}, config.ListFormatWishlist, ""},
	}
	for _, c := range cases {
		t.Run(strings.Join(c.args, " "), func(t *testing.T) {
			inv := parse(t, c.args...)
			if !inv.List {
				t.Error("List must be set")
			}
			if inv.ListFormat != c.format {
				t.Errorf("format = %d, want %d", inv.ListFormat, c.format)
			}
			if inv.Unsupported != c.unsup {
				t.Errorf("unsupported = %q, want %q", inv.Unsupported, c.unsup)
			}
		})
	}

	// --list must not swallow a following flag as its format.
	inv := parse(t, "--list", "--no-color")
	if inv.ListFormat != config.ListFormatGames || inv.Config.Color {
		t.Errorf("--list --no-color parsed as %+v", inv)
	}
}

func TestParseUnsupportedAndUnknown(t *testing.T) {
	for _, flag := range []string{"--save-config", "--reset-config", "--update-cache", "--download", "--repair"} {
		inv := parse(t, flag)
		if inv.Unsupported != flag {
			t.Errorf("%s: unsupported = %q", flag, inv.Unsupported)
		}
	}
	if _, err := Parse([]string{"--nonsense"}, testDefaults()); err == nil {
		t.Error("unknown option must fail")
	}
	if _, err := Parse([]string{"positional"}, testDefaults()); err == nil {
		t.Error("positional argument must fail")
	}
	if _, err := Parse([]string{"--game"}, testDefaults()); err == nil {
		t.Error("missing option value must fail")
	}
	if _, err := Parse([]string{"--retries", "x"}, testDefaults()); err == nil {
		t.Error("non-numeric --retries must fail")
	}
}

func TestParseLoginAndFilterFlags(t *testing.T) {
	inv := parse(t, "--login", "--browser-login", "--login-email", "a@b", "--login-password", "pw",
		"--game", "alpha", "--game-list", "/tmp/list", "--tags", "a,b",
		"--updated", "--new", "--no-platform-detection", "--ignore-dlc-count",
		"--cacert", "/tmp/ca.pem", "--unit-format", "si")
	if !inv.Config.Login || !inv.Config.ForceBrowserLogin {
		t.Errorf("login flags = %+v", inv.Config)
	}
	if inv.Config.Email != "a@b" || inv.Config.Password != "pw" {
		t.Errorf("credentials = %q/%q", inv.Config.Email, inv.Config.Password)
	}
	if inv.Config.GameRegex != "alpha" || inv.Config.GameListFilePath != "/tmp/list" {
		t.Errorf("filters = %q/%q", inv.Config.GameRegex, inv.Config.GameListFilePath)
	}
	if len(inv.Config.DownloadConfig.Tags) != 2 {
		t.Errorf("tags = %v", inv.Config.DownloadConfig.Tags)
	}
	if !inv.Config.Updated || !inv.Config.New {
		t.Error("--updated/--new must set their fields")
	}
	if inv.Config.PlatformDetection {
		t.Error("--no-platform-detection must clear the flag")
	}
	if inv.Config.IgnoreDLCCountRegex != ".*" {
		t.Errorf("ignore-dlc-count default = %q, want .*", inv.Config.IgnoreDLCCountRegex)
	}
	if inv.Config.Curl.CACertPath != "/tmp/ca.pem" {
		t.Errorf("cacert = %q", inv.Config.Curl.CACertPath)
	}
	if inv.Config.UnitFormat != config.UnitFormatSI {
		t.Errorf("unit format = %d", inv.Config.UnitFormat)
	}
}

func TestParseIgnoresExtraDashesAndEquals(t *testing.T) {
	inv := parse(t, "-directory=/games", "--retries=2")
	if inv.Config.Directories.Directory != "/games" || inv.Config.Retries != 2 {
		t.Errorf("config = %+v", inv.Config)
	}
}

func TestParseHelpVersionCheck(t *testing.T) {
	inv := parse(t, "--help")
	if !inv.Help {
		t.Error("--help not detected")
	}
	inv = parse(t, "--version")
	if !inv.Version {
		t.Error("--version not detected")
	}
	inv = parse(t, "--check-login-status")
	if !inv.CheckLoginStatus {
		t.Error("--check-login-status not detected")
	}
}

func TestNewConfigPaths(t *testing.T) {
	cfg := config.NewConfig("/cfg", "/cache")
	if cfg.CacheDirectory != "/cache/goggo" || cfg.XMLDirectory != "/cache/goggo/xml" {
		t.Errorf("cache paths = %q / %q", cfg.CacheDirectory, cfg.XMLDirectory)
	}
	if cfg.ConfigFilePath != "/cfg/goggo/config.cfg" {
		t.Errorf("config path = %q", cfg.ConfigFilePath)
	}
	if cfg.VersionString != config.VersionString || cfg.VersionNumber != config.Version {
		t.Errorf("version = %q / %q", cfg.VersionString, cfg.VersionNumber)
	}
}

// TestIdentityIsSeparateFromCompatibility locks the three-layer identity: the
// program presents itself, and names the upstream release it tracks only as a
// compatibility baseline.
func TestIdentityIsSeparateFromCompatibility(t *testing.T) {
	if config.Version == config.UpstreamCompatibilityVersion {
		t.Fatalf("own version %q must not equal the compatibility baseline", config.Version)
	}
	if !strings.HasPrefix(config.VersionString, config.ProgramName+" ") {
		t.Errorf("VersionString = %q, want it to start with the program name", config.VersionString)
	}
	if strings.Contains(config.VersionString, config.UpstreamName) {
		t.Errorf("VersionString = %q must not present the upstream project as our identity", config.VersionString)
	}

	ua := config.DefaultUserAgent()
	if !strings.HasPrefix(ua, config.ProgramName+"/"+config.Version) {
		t.Errorf("UserAgent = %q, want it to start with %q", ua, config.ProgramName+"/"+config.Version)
	}
	if strings.Contains(ua, config.UpstreamName) {
		t.Errorf("UserAgent = %q must not carry the upstream product name", ua)
	}
}
