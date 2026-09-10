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
	// "." becomes "./" : Parse normalises directory arguments after the flags
	// are read (main.cpp:647).
	if inv.Config.Directories.Directory != "./" {
		t.Errorf("directory default = %q, want ./", inv.Config.Directories.Directory)
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
	// Parse normalises directory arguments (main.cpp:647), so the value gains a
	// trailing separator.
	if inv.Config.Directories.Directory != "/games/" {
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

// TestParseBrowserLoginImpliesLogin locks main.cpp:472-475: --browser-login
// selects the login path itself, so it must set Login too. Without it, Open's
// trigger (cfg.Login || !LoggedIn) would short-circuit on a stored session and
// a requested browser login would never run.
func TestParseBrowserLoginImpliesLogin(t *testing.T) {
	inv := parse(t, "--browser-login")
	if !inv.Config.ForceBrowserLogin {
		t.Error("--browser-login must set ForceBrowserLogin")
	}
	if !inv.Config.Login {
		t.Error("--browser-login must also set Login (main.cpp:472-475)")
	}

	// The implication is one-way: --login must not force the browser flow.
	inv = parse(t, "--login")
	if !inv.Config.Login {
		t.Error("--login must set Login")
	}
	if inv.Config.ForceBrowserLogin {
		t.Error("--login must not set ForceBrowserLogin")
	}
}

// TestParseLogout locks the goggo-native --logout flag: it selects the local
// logout action and must not imply a login.
func TestParseLogout(t *testing.T) {
	inv := parse(t, "--logout")
	if !inv.Logout {
		t.Error("--logout must set Logout")
	}
	if inv.Config.Login || inv.Config.ForceBrowserLogin {
		t.Error("--logout must not imply a login")
	}
}

// TestParseLogoutConflicts locks the Q1/Q5 ruling: --logout is a mutation that
// clears the whole local login state, so pairing it with another action is
// refused by name rather than resolved silently. --check-login-status is a
// query and is deliberately NOT a conflict (query > mutation, Q7), and
// --help/--version are answered by the dispatcher before anything runs.
func TestParseLogoutConflicts(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string // empty means the command line is accepted
	}{
		{name: "alone", args: []string{"--logout"}},
		{name: "with login", args: []string{"--logout", "--login"}, want: "--logout cannot be combined with --login"},
		{name: "login first", args: []string{"--login", "--logout"}, want: "--logout cannot be combined with --login"},
		{name: "with browser login", args: []string{"--logout", "--browser-login"}, want: "--logout cannot be combined with --browser-login"},
		{name: "with list", args: []string{"--logout", "--list"}, want: "--logout cannot be combined with --list"},
		{name: "with list format", args: []string{"--logout", "--list", "tags"}, want: "--logout cannot be combined with --list"},
		{name: "with check-login-status", args: []string{"--logout", "--check-login-status"}},
		{name: "with help", args: []string{"--logout", "--help"}},
		{name: "with version", args: []string{"--logout", "--version"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			inv, err := Parse(c.args, testDefaults())
			if c.want == "" {
				if err != nil {
					t.Fatalf("Parse(%v) must be accepted: %v", c.args, err)
				}
				if !inv.Logout {
					t.Errorf("Parse(%v) must still set Logout", c.args)
				}
				return
			}
			if err == nil {
				t.Fatalf("Parse(%v) must be refused", c.args)
			}
			if err.Error() != c.want {
				t.Errorf("err = %q, want %q", err.Error(), c.want)
			}
		})
	}
}

func TestParseIgnoresExtraDashesAndEquals(t *testing.T) {
	inv := parse(t, "-directory=/games", "--retries=2")
	// The directory is normalised like any other (main.cpp:647).
	if inv.Config.Directories.Directory != "/games/" || inv.Config.Retries != 2 {
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

// TestParseGalaxyCommands locks the two Galaxy commands and their two settings
// (main.cpp:329,334,340,346), including the two defaults this layer owns
// because the C++ front end declares them next to the options.
func TestParseGalaxyCommands(t *testing.T) {
	inv := parse(t)
	if inv.Config.GalaxyBuildSortingOrder != defaultGalaxyBuildSort {
		t.Errorf("galaxy-builds-sort default = %q, want %q",
			inv.Config.GalaxyBuildSortingOrder, defaultGalaxyBuildSort)
	}
	if inv.Config.DownloadConfig.GalaxyPlatform != config.PlatformWindows {
		t.Errorf("galaxy-platform default = %d, want the Windows value",
			inv.Config.DownloadConfig.GalaxyPlatform)
	}
	if inv.GalaxyShowBuilds != "" || inv.GalaxyListCDNs != "" {
		t.Error("no Galaxy command may be requested by default")
	}

	// The argument is kept verbatim: splitting it into product and build is the
	// dispatcher's job (main.cpp:840-845).
	inv = parse(t, "--galaxy-show-builds", "12345/2")
	if inv.GalaxyShowBuilds != "12345/2" {
		t.Errorf("--galaxy-show-builds = %q, want the raw argument", inv.GalaxyShowBuilds)
	}

	inv = parse(t, "--galaxy-list-cdns=12345", "--galaxy-builds-sort", "date", "--galaxy-platform", "linux")
	if inv.GalaxyListCDNs != "12345" {
		t.Errorf("--galaxy-list-cdns = %q, want the value after =", inv.GalaxyListCDNs)
	}
	if inv.Config.GalaxyBuildSortingOrder != "date" {
		t.Errorf("galaxy-builds-sort = %q, want date", inv.Config.GalaxyBuildSortingOrder)
	}
	if inv.Config.DownloadConfig.GalaxyPlatform != config.PlatformLinux {
		t.Errorf("galaxy-platform = %d, want the Linux value", inv.Config.DownloadConfig.GalaxyPlatform)
	}

	// An unrecognised order is accepted: the C++ source only acts on two of them
	// and leaves the list alone otherwise (downloader.cpp:6826-6830).
	inv = parse(t, "--galaxy-builds-sort", "whatever")
	if inv.Config.GalaxyBuildSortingOrder != "whatever" {
		t.Errorf("galaxy-builds-sort = %q, want the value stored as given",
			inv.Config.GalaxyBuildSortingOrder)
	}
}

// TestParseGalaxyErrors locks the two rejected shapes: a missing value and an
// unmatched platform. The platform rejection is a recorded difference — the C++
// Util::getOptionValue returns 0 for an unknown name and the front end then
// treats it as Windows, while --platform already rejects it here.
func TestParseGalaxyErrors(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{name: "missing value", args: []string{"--galaxy-show-builds"}, want: "requires a value"},
		{name: "missing sort value", args: []string{"--galaxy-builds-sort"}, want: "requires a value"},
		{
			name: "unknown platform", args: []string{"--galaxy-platform", "nope"},
			want: "invalid value for --galaxy-platform",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse(c.args, testDefaults())
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want it to contain %q", err, c.want)
			}
		})
	}
}

// TestParseGalaxyInstallDefaults locks the defaults the option layer owns
// (main.cpp:341-345). Two of them are the positive side of a negated option,
// which matters because their zero values are the opposite.
func TestParseGalaxyInstallDefaults(t *testing.T) {
	inv := parse(t)

	if inv.GalaxyInstall != "" {
		t.Errorf("GalaxyInstall = %q, want no command", inv.GalaxyInstall)
	}
	if inv.Config.DownloadConfig.GalaxyLanguage != config.LangEN {
		t.Errorf("galaxy language = %d, want the English flag", inv.Config.DownloadConfig.GalaxyLanguage)
	}
	if inv.Config.DownloadConfig.GalaxyArch != config.ArchX64 {
		t.Errorf("galaxy arch = %d, want the 64-bit flag", inv.Config.DownloadConfig.GalaxyArch)
	}
	priority := inv.Config.DownloadConfig.GalaxyCDNPriority
	want := []string{"edgecast", "akamai_edgecast_proxy", "fastly"}
	if len(priority) != len(want) {
		t.Fatalf("galaxy cdn priority = %v, want %v", priority, want)
	}
	for i := range want {
		if priority[i] != want[i] {
			t.Errorf("galaxy cdn priority = %v, want %v", priority, want)
		}
	}
	if inv.Config.Directories.GalaxyInstallSubdir != "%install_dir%" {
		t.Errorf("install subdir = %q, want the template default", inv.Config.Directories.GalaxyInstallSubdir)
	}
	// --no-subdirectories and --galaxy-no-dependencies are negations, so their
	// defaults are true — the zero value here would be wrong.
	if !inv.Config.Directories.SubDirectories {
		t.Error("SubDirectories = false, want true by default")
	}
	if !inv.Config.DownloadConfig.GalaxyDependencies {
		t.Error("GalaxyDependencies = false, want true by default")
	}
}

// TestParseGalaxyInstallFlags locks the seven flags, including that the command
// argument is kept whole: splitting it is the dispatcher's job.
func TestParseGalaxyInstallFlags(t *testing.T) {
	inv := parse(t, "--galaxy-install", "12345/2", "--galaxy-language", "de",
		"--galaxy-arch", "x86", "--galaxy-cdn-priority", "a,,b",
		"--subdir-galaxy-install", "%product_id%",
		"--galaxy-no-dependencies", "--no-subdirectories")

	if inv.GalaxyInstall != "12345/2" {
		t.Errorf("GalaxyInstall = %q, want the raw argument", inv.GalaxyInstall)
	}
	if inv.Config.DownloadConfig.GalaxyLanguage != config.LangDE {
		t.Errorf("galaxy language = %d, want the German flag", inv.Config.DownloadConfig.GalaxyLanguage)
	}
	if inv.Config.DownloadConfig.GalaxyArch != config.ArchX86 {
		t.Errorf("galaxy arch = %d, want the 32-bit flag", inv.Config.DownloadConfig.GalaxyArch)
	}
	// The empty element is dropped, exactly as Util::tokenize does.
	if priority := inv.Config.DownloadConfig.GalaxyCDNPriority; len(priority) != 2 ||
		priority[0] != "a" || priority[1] != "b" {
		t.Errorf("galaxy cdn priority = %v, want [a b]", priority)
	}
	if inv.Config.Directories.GalaxyInstallSubdir != "%product_id%" {
		t.Errorf("install subdir = %q", inv.Config.Directories.GalaxyInstallSubdir)
	}
	if inv.Config.DownloadConfig.GalaxyDependencies {
		t.Error("--galaxy-no-dependencies must clear the setting")
	}
	if inv.Config.Directories.SubDirectories {
		t.Error("--no-subdirectories must clear the setting")
	}
}

// TestParseGalaxyLanguageArchUnmatched locks the deliberate difference from
// --galaxy-platform: an unrecognised language or architecture is not refused.
// The C++ source does not refuse it either, and the Galaxy layer turns a
// missing match into its own default (downloader.cpp:3904-3922).
func TestParseGalaxyLanguageArchUnmatched(t *testing.T) {
	inv := parse(t, "--galaxy-language", "nope")
	if inv.Config.DownloadConfig.GalaxyLanguage != 0 {
		t.Errorf("galaxy language = %d, want no match", inv.Config.DownloadConfig.GalaxyLanguage)
	}

	// "all" and an unmatched value both mean 64-bit (main.cpp:579-580).
	for _, value := range []string{"nope", "all"} {
		inv = parse(t, "--galaxy-arch", value)
		if inv.Config.DownloadConfig.GalaxyArch != config.ArchX64 {
			t.Errorf("--galaxy-arch %s = %d, want the 64-bit flag",
				value, inv.Config.DownloadConfig.GalaxyArch)
		}
	}

	// The integer channel is real: std::stoi runs before the table walk, so a
	// number selects the entry carrying that flag value.
	inv = parse(t, "--galaxy-language", "4")
	if inv.Config.DownloadConfig.GalaxyLanguage != config.LangFR {
		t.Errorf("galaxy language 4 = %d, want the French flag",
			inv.Config.DownloadConfig.GalaxyLanguage)
	}
}

// TestEnsureTrailingSlash locks the post-parse normalisation (main.cpp:28-40).
// Only a forward slash counts, so a backslash path gains one.
func TestEnsureTrailingSlash(t *testing.T) {
	cases := []struct{ in, want string }{
		{in: "", want: "./"},
		{in: ".", want: "./"},
		{in: "/games", want: "/games/"},
		{in: "/games/", want: "/games/"},
		{in: `C:\games`, want: `C:\games/`},
	}
	for _, c := range cases {
		if got := ensureTrailingSlash(c.in, "./"); got != c.want {
			t.Errorf("ensureTrailingSlash(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if got := ensureTrailingSlash("", "/fallback/"); got != "/fallback/" {
		t.Errorf("fallback = %q, want /fallback/", got)
	}
}

// TestParseCheckFreeSpace locks the plan-builder's space gate option: it is a
// plain boolean, off by default (main.cpp:319).
func TestParseCheckFreeSpace(t *testing.T) {
	if parse(t).Config.DownloadConfig.FreeSpaceCheck {
		t.Error("FreeSpaceCheck = true, want the false default")
	}
	if !parse(t, "--check-free-space").Config.DownloadConfig.FreeSpaceCheck {
		t.Error("--check-free-space must set FreeSpaceCheck")
	}
}

// TestParseThreadsProgressInterval locks the execution parameters the transfer
// consumes: the thread count passes through (the run clamps it to the task
// count) and the progress interval is clamped into 1..10000 ms (main.cpp:313).
func TestParseThreadsProgressInterval(t *testing.T) {
	inv := parse(t)
	if inv.Config.Threads != 4 {
		t.Errorf("threads default = %d, want 4", inv.Config.Threads)
	}
	if inv.Config.ProgressInterval != 100 {
		t.Errorf("progress interval default = %d, want 100", inv.Config.ProgressInterval)
	}

	inv = parse(t, "--threads", "8", "--progress-interval", "5000")
	if inv.Config.Threads != 8 || inv.Config.ProgressInterval != 5000 {
		t.Errorf("threads/interval = %d/%d, want 8/5000",
			inv.Config.Threads, inv.Config.ProgressInterval)
	}

	// Out-of-range intervals fall back to the default, and a negative thread
	// count is refused the way an unsigned option is.
	inv = parse(t, "--progress-interval", "0")
	if inv.Config.ProgressInterval != 100 {
		t.Errorf("interval 0 = %d, want the 100 fallback", inv.Config.ProgressInterval)
	}
	inv = parse(t, "--progress-interval", "20000")
	if inv.Config.ProgressInterval != 100 {
		t.Errorf("interval 20000 = %d, want the 100 fallback", inv.Config.ProgressInterval)
	}
	if _, err := Parse([]string{"--threads", "-1"}, testDefaults()); err == nil {
		t.Error("--threads -1 must be refused")
	}
}
