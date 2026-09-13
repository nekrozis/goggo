package cli

import (
	"strings"
	"testing"

	"github.com/nekrozis/goggo/internal/config"
	"github.com/nekrozis/goggo/internal/util"
)

func testDefaults() config.Config {
	return config.NewConfig("/cfg", "/cache")
}

// parseOpts is the migrated entry point of these tests: the old Parse is gone
// (CLI1 S2), and every case below now speaks the command vocabulary.
func parseOpts(t *testing.T, args ...string) invocation {
	t.Helper()
	inv, err := parseArgs(args, testDefaults())
	if err != nil {
		t.Fatalf("parseArgs(%v): %v", args, err)
	}
	return inv
}

// TestParseDefaultsComeFromConfig locks the precedence order: the configuration
// handed in is the base, the parser installs only the defaults it declares, and
// a value the caller set survives unless a flag overrides it.
func TestParseDefaultsComeFromConfig(t *testing.T) {
	cfg := testDefaults()
	cfg.Threads = 8
	cfg.Color = false
	inv, err := parseArgs([]string{"install", "123"}, cfg)
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if inv.cfg.Threads != 8 || inv.cfg.Color {
		t.Errorf("caller values were lost: threads=%d color=%v", inv.cfg.Threads, inv.cfg.Color)
	}
	if inv.cfg.Directories.Directory != "./" {
		t.Errorf("directory = %q, want the default normalised to ./", inv.cfg.Directories.Directory)
	}
	if inv.cfg.UnitFormat != config.UnitFormatIEC || inv.cfg.Retries != 3 {
		t.Errorf("config defaults missing: unit=%d retries=%d", inv.cfg.UnitFormat, inv.cfg.Retries)
	}
}

// TestParseFlagsOverrideDefaults locks that a flag wins over both layers.
func TestParseFlagsOverrideDefaults(t *testing.T) {
	inv := parseOpts(t, "install", "123", "--threads", "16", "--retries", "5", "--wait", "250",
		"--unit-format", "SI", "--directory", "/games", "--no-color", "--no-unicode", "--timeout", "30")
	if inv.cfg.Threads != 16 || inv.cfg.Retries != 5 || inv.cfg.Wait != 250 {
		t.Errorf("numeric flags: %+v", inv.cfg)
	}
	if inv.cfg.UnitFormat != config.UnitFormatSI || inv.cfg.Curl.Timeout != 30 {
		t.Errorf("unit/timeout: %+v", inv.cfg)
	}
	if inv.cfg.Directories.Directory != "/games/" || inv.cfg.Color || inv.cfg.Unicode {
		t.Errorf("directory/rendering: %+v", inv.cfg.Directories)
	}
}

// TestParsePlatformAndLanguage locks the install-side platform selection and its
// validation (the listing side lives on list games, see parse_test.go).
func TestParsePlatformAndLanguage(t *testing.T) {
	inv := parseOpts(t, "install", "123", "--platform", "linux", "--language", "fr", "--arch", "x86")
	if inv.cfg.DownloadConfig.GalaxyPlatform != config.PlatformLinux {
		t.Errorf("platform = %#x, want linux", inv.cfg.DownloadConfig.GalaxyPlatform)
	}
	if inv.cfg.DownloadConfig.GalaxyArch != config.ArchX86 {
		t.Errorf("arch = %#x, want x86", inv.cfg.DownloadConfig.GalaxyArch)
	}
	// A language with no match leaves 0, which the Galaxy layer reads as
	// English — the upstream behaviour (downloader.cpp:3904-3913).
	if got := parseOpts(t, "install", "123", "--language", "nonsense").cfg.DownloadConfig.GalaxyLanguage; got != 0 {
		t.Errorf("unmatched language = %#x, want 0 (English)", got)
	}
	_, err := parseArgs([]string{"install", "123", "--platform", "nonsense"}, testDefaults())
	if err == nil {
		t.Error("an unmatched platform must be refused")
	}
	if !isUsageError(err) {
		t.Error("the refusal must be a usage error")
	}
}

// TestParseIncludeExclude locks the include mask (verify honours it; the orphan
// walk does not, and the option table says so).
func TestParseIncludeExclude(t *testing.T) {
	inv := parseOpts(t, "verify", "123", "--include", "installers,patches", "--exclude", "patches")
	want := optionMask("installers,patches", config.IncludeOptions) &^ optionMask("patches", config.IncludeOptions)
	if inv.cfg.DownloadConfig.Include != want {
		t.Errorf("include = %#x, want %#x", inv.cfg.DownloadConfig.Include, want)
	}
	// --include alone keeps everything the mask lists.
	only := parseOpts(t, "verify", "123", "--include", "installers")
	if only.cfg.DownloadConfig.Include == 0 || only.cfg.DownloadConfig.Include&^config.IncludeAllMask() != 0 {
		t.Errorf("include = %#x, want a subset of all", only.cfg.DownloadConfig.Include)
	}
}

// TestParseListResources locks the listing vocabulary: the resource is a
// subcommand, and each one maps onto the catalogue's format mask.
func TestParseListResources(t *testing.T) {
	for _, tc := range []struct {
		resource string
		cmd      commandID
		format   uint32
	}{
		{"games", cmdListGames, config.ListFormatGames},
		{"tags", cmdListTags, config.ListFormatTags},
		{"wishlist", cmdListWishlist, config.ListFormatWishlist},
	} {
		inv := parseOpts(t, "list", tc.resource)
		if inv.cmd != tc.cmd || listFormat(inv.cmd) != tc.format {
			t.Errorf("list %s = cmd %d format %#x", tc.resource, inv.cmd, listFormat(inv.cmd))
		}
	}
	// The removed option that used to carry this is unknown, with a hint.
	err := mustUsageError(t, "--list", "tags")
	if !strings.Contains(err.Error(), "unknown option") || !strings.Contains(err.Error(), "goggo list") {
		t.Errorf("error = %v, want the removal hint", err)
	}
}

// TestParseUnknownAndRemovedOptions locks the failure shape and the migration
// hints (D2/D11): a removed option is still an error, and the hint only says
// where the capability went.
func TestParseUnknownAndRemovedOptions(t *testing.T) {
	for _, c := range []struct {
		args []string
		hint string
	}{
		{[]string{"--galaxy-install", "123"}, "goggo install <game>"},
		{[]string{"--check-orphans", "x"}, "goggo orphans check <game>"},
		{[]string{"--status"}, "goggo verify <game>"},
		{[]string{"--download"}, ""},
		{[]string{"--repair"}, ""},
		{[]string{"--verbosity", "1"}, "goggo -v"},
	} {
		_, err := parseArgs(c.args, testDefaults())
		if err == nil || !isUsageError(err) {
			t.Fatalf("parseArgs(%v) = %v, want a usage error", c.args, err)
		}
		if !strings.Contains(err.Error(), "unknown option") {
			t.Errorf("parseArgs(%v) error = %v, want an unknown option", c.args, err)
		}
		if c.hint != "" && !strings.Contains(err.Error(), c.hint) {
			t.Errorf("parseArgs(%v) error = %v, want the hint %q", c.args, err, c.hint)
		}
	}
	if _, err := parseArgs([]string{"install", "123", "--nonsense"}, testDefaults()); err == nil {
		t.Error("an unknown option must be refused")
	}
	if _, err := parseArgs([]string{"install"}, testDefaults()); err == nil {
		t.Error("a missing game must be refused")
	}
	if _, err := parseArgs([]string{"install", "123", "--retries", "x"}, testDefaults()); err == nil {
		t.Error("a malformed value must be refused")
	}
}

// TestParseLoginAndFilterFlags locks the auth command's inputs and the listing
// filters (D7: they belong to the command).
func TestParseLoginAndFilterFlags(t *testing.T) {
	inv := parseOpts(t, "auth", "login", "--email", "user@example.com")
	if inv.cmd != cmdAuthLogin || inv.cfg.Email != "user@example.com" {
		t.Errorf("auth login = cmd %d email %q", inv.cmd, inv.cfg.Email)
	}

	filters := parseOpts(t, "list", "games",
		"--game", "^The", "--game-list", "/tmp/list.txt", "--updated", "--new",
		"--include-hidden-products", "--ignore-dlc-count", "--ignore-dlc-count=^Skip")
	if filters.cfg.GameRegex != "^The" || filters.cfg.GameListFilePath != "/tmp/list.txt" {
		t.Errorf("game filters: %+v", filters.cfg)
	}
	if !filters.cfg.Updated || !filters.cfg.New || !filters.cfg.IncludeHiddenProducts {
		t.Errorf("boolean filters: %+v", filters.cfg)
	}
	// An implicit value takes the documented default, and an explicit one wins.
	if got := parseOpts(t, "list", "games", "--ignore-dlc-count").cfg.IgnoreDLCCountRegex; got != ".*" {
		t.Errorf("implicit ignore-dlc-count = %q, want .*", got)
	}
	if got := parseOpts(t, "list", "games", "--ignore-dlc-count=^Skip").cfg.IgnoreDLCCountRegex; got != "^Skip" {
		t.Errorf("explicit ignore-dlc-count = %q", got)
	}
}

// TestParseBrowserLogin locks --browser: it selects the flow. The "and therefore
// log in" half lives in the dispatcher (auth login always sets cfg.Login), not
// in the option.
func TestParseBrowserLogin(t *testing.T) {
	inv := parseOpts(t, "auth", "login", "--browser")
	if !inv.cfg.ForceBrowserLogin {
		t.Error("--browser must force the browser flow")
	}
}

// TestParseLogout locks the local logout command and the grammar that replaced
// the old conflict checks: one command per line, so there is nothing to
// conflict with.
func TestParseLogout(t *testing.T) {
	if inv := parseOpts(t, "auth", "logout"); inv.cmd != cmdAuthLogout {
		t.Errorf("auth logout = cmd %d", inv.cmd)
	}
	for _, args := range [][]string{
		{"auth", "logout", "--login"}, // the removed option
		{"auth", "logout", "login"},   // a second verb is an argument
	} {
		_, err := parseArgs(args, testDefaults())
		if err == nil || !isUsageError(err) {
			t.Errorf("parseArgs(%v) = %v, want a usage error", args, err)
		}
	}
}

// TestParseEqualsForm locks --name=value, the form the parser accepts for every
// option that takes a value.
func TestParseEqualsForm(t *testing.T) {
	inv := parseOpts(t, "install", "123", "--threads=8", "--platform=windows", "--directory=/games")
	if inv.cfg.Threads != 8 || inv.cfg.Directories.Directory != "/games/" {
		t.Errorf("equals form: threads=%d directory=%q", inv.cfg.Threads, inv.cfg.Directories.Directory)
	}
	if inv.cfg.DownloadConfig.GalaxyPlatform != config.PlatformWindows {
		t.Error("--platform=windows did not select windows")
	}
}

// TestParseShowCommands locks the show family and the target split it carries.
func TestParseShowCommands(t *testing.T) {
	if inv := parseOpts(t, "show", "builds", "123"); inv.cmd != cmdShowBuilds || inv.target.Build != "" {
		t.Errorf("show builds = cmd %d target %+v", inv.cmd, inv.target)
	}
	manifest := parseOpts(t, "show", "manifest", "123/2")
	if manifest.cmd != cmdShowManifest || manifest.target.Product != "123" || manifest.target.Build != "2" {
		t.Errorf("show manifest = cmd %d target %+v", manifest.cmd, manifest.target)
	}
	if inv := parseOpts(t, "show", "cdns", "123"); inv.cmd != cmdShowCDNs {
		t.Errorf("show cdns = cmd %d", inv.cmd)
	}
	// "show builds" lists builds; a build in the argument is a different
	// command, not a filter (the split that replaced upstream's dual meaning).
	if _, err := parseArgs([]string{"show", "builds", "123/2"}, testDefaults()); err == nil {
		t.Error("show builds with a build must be refused")
	}
}

// TestParseTargetErrors locks the target's shape rules (D2: refuse rather than
// silently reinterpret).
func TestParseTargetErrors(t *testing.T) {
	for _, arg := range []string{"", "/2", "1/2/3", "/"} {
		if _, err := parseArgs([]string{"install", arg}, testDefaults()); err == nil {
			t.Errorf("install %q must be refused", arg)
		}
	}
	// An empty build is "no build given", the way the upstream tokenizer reads it.
	if inv := parseOpts(t, "install", "1/"); inv.target.Product != "1" || inv.target.Build != "" {
		t.Errorf("install 1/ = %+v, want product 1 with no build", inv.target)
	}
}

// TestParseInstallDefaults locks the defaults the parser owns for an install.
func TestParseInstallDefaults(t *testing.T) {
	inv := parseOpts(t, "install", "123")
	if inv.cfg.GalaxyBuildSortingOrder != defaultGalaxyBuildSort {
		t.Errorf("sort = %q", inv.cfg.GalaxyBuildSortingOrder)
	}
	if inv.cfg.DownloadConfig.GalaxyPlatform != config.PlatformWindows ||
		inv.cfg.DownloadConfig.GalaxyArch != config.ArchX64 {
		t.Errorf("platform/arch: %+v", inv.cfg.DownloadConfig)
	}
	if !inv.cfg.Directories.SubDirectories || !inv.cfg.DownloadConfig.GalaxyDependencies {
		t.Error("the negations' defaults must be the positive values")
	}
	if inv.cfg.Directories.GalaxyInstallSubdir != defaultGalaxyInstallSubdir {
		t.Errorf("subdir = %q", inv.cfg.Directories.GalaxyInstallSubdir)
	}
	want := util.Split(defaultGalaxyCDNPriority, ",")
	if len(inv.cfg.DownloadConfig.GalaxyCDNPriority) != len(want) {
		t.Errorf("cdn priority = %v, want %v", inv.cfg.DownloadConfig.GalaxyCDNPriority, want)
	}
}

// TestParseInstallFlags locks the install domain flags: they configure how the
// plan is resolved, not what is installed.
func TestParseInstallFlags(t *testing.T) {
	inv := parseOpts(t, "install", "123", "--install-dir", "My Game", "--no-subdirectories",
		"--cdn-priority", "fastly", "--no-dependencies", "--check-free-space")
	if inv.cfg.Directories.GalaxyInstallSubdir != "My Game" {
		t.Errorf("install dir = %q", inv.cfg.Directories.GalaxyInstallSubdir)
	}
	if inv.cfg.Directories.SubDirectories || inv.cfg.DownloadConfig.GalaxyDependencies {
		t.Error("the negations did not clear their settings")
	}
	if len(inv.cfg.DownloadConfig.GalaxyCDNPriority) != 1 || inv.cfg.DownloadConfig.GalaxyCDNPriority[0] != "fastly" {
		t.Errorf("cdn priority = %v", inv.cfg.DownloadConfig.GalaxyCDNPriority)
	}
	if !inv.cfg.DownloadConfig.FreeSpaceCheck {
		t.Error("--check-free-space did not set the gate")
	}
	// The same options belong to verify (the install root must be identical).
	if inv := parseOpts(t, "verify", "123", "--install-dir", "My Game"); inv.cfg.Directories.GalaxyInstallSubdir != "My Game" {
		t.Error("verify must resolve the same install root")
	}
}

// TestEnsureTrailingSlash keeps the directory normalisation the install path is
// concatenated from.
func TestEnsureTrailingSlash(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"/games", "/games/"},
		{"/games/", "/games/"},
		{"games", "games/"},
	} {
		if got := ensureTrailingSlash(c.in, "./"); got != c.want {
			t.Errorf("ensureTrailingSlash(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if got := ensureTrailingSlash("", "/fallback/"); got != "/fallback/" {
		t.Errorf("empty path = %q, want the fallback", got)
	}
}

// TestParseThreadsAndProgressInterval locks the two numeric rendering/transfer
// knobs the parser owns; the clamp itself is locked in parse_test.go.
func TestParseThreadsAndProgressInterval(t *testing.T) {
	inv := parseOpts(t, "install", "123", "--threads", "8", "--progress-interval", "50")
	if inv.cfg.Threads != 8 || inv.cfg.ProgressInterval != 50 {
		t.Errorf("threads/interval = %d/%d", inv.cfg.Threads, inv.cfg.ProgressInterval)
	}
	if _, err := parseArgs([]string{"install", "123", "--threads", "-1"}, testDefaults()); err == nil {
		t.Error("a negative thread count must be refused")
	}
	if _, err := parseArgs([]string{"install", "123", "--progress-interval", "x"}, testDefaults()); err == nil {
		t.Error("a malformed interval must be refused")
	}
}

// TestParseOrphansOptions locks the orphan command's options: the two filter
// files are its own, and the include mask is not (upstream checks everything).
func TestParseOrphansOptions(t *testing.T) {
	inv := parseOpts(t, "orphans", "check", "123", "--ignorelist", "/tmp/ignore.txt", "--blacklist", "/tmp/black.txt")
	if inv.cfg.IgnorelistFilePath != "/tmp/ignore.txt" || inv.cfg.BlacklistFilePath != "/tmp/black.txt" {
		t.Errorf("filter paths: %q / %q", inv.cfg.IgnorelistFilePath, inv.cfg.BlacklistFilePath)
	}
	remove := parseOpts(t, "orphans", "remove", "123", "--yes")
	if remove.cmd != cmdOrphansRemove || !remove.yes {
		t.Errorf("orphans remove = cmd %d yes=%v", remove.cmd, remove.yes)
	}
	if _, err := parseArgs([]string{"orphans", "check", "123", "--include", "all"}, testDefaults()); err == nil {
		t.Error("the orphan walk must not accept the include mask")
	}
}

// TestNewConfigPaths locks the config-domain defaults (unchanged by the parser
// rework, kept here so the file still covers them).
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
