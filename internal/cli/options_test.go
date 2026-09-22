package cli

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/nekrozis/goggo/internal/config"
	"github.com/nekrozis/goggo/internal/core"
	"github.com/nekrozis/goggo/internal/util"
)

func testDefaults() config.Config {
	return config.NewConfig("/cfg", "/cache")
}

// parseOpts is the entry point of these tests: every case below speaks the
// command vocabulary.
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
	if inv.cfg.Directories.Directory != "." {
		t.Errorf("directory = %q, want the default normalised to .", inv.cfg.Directories.Directory)
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
	if inv.cfg.Directories.Directory != filepath.Clean("/games") || inv.cfg.Color || inv.cfg.Unicode {
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
	// An arch with no match falls back to 64-bit rather than failing: the
	// Galaxy layer has no "unknown arch" state to report.
	if got := parseOpts(t, "install", "123", "--arch", "nonsense").cfg.DownloadConfig.GalaxyArch; got != config.ArchX64 {
		t.Errorf("unmatched arch = %#x, want the x64 fallback", got)
	}
	// A language with no match leaves 0, which the Galaxy layer reads as
	// English.
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
		if got := listFormat(inv.cmd); inv.cmd != tc.cmd || got != tc.format {
			t.Errorf("list %s = cmd %d format %#x", tc.resource, inv.cmd, got)
		}
	}
	// The removed option that used to carry this is unknown, with a hint.
	err := mustUsageError(t, "--list", "tags")
	if !strings.Contains(err.Error(), "unknown option") || !strings.Contains(err.Error(), "goggo list") {
		t.Errorf("error = %v, want the removal hint", err)
	}
}

// TestParseUnknownAndRemovedOptions locks the failure shape and the migration
// hints: a removed option is still an error, the hint only says where the
// capability went, and nothing is translated into an invocation.
//
// The credential options this CLI refuses to introduce are the parser's other
// refusal case — see TestCredentialOptionsAreNeverAdvertised, where their
// absence of a hint is the point.
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
		inv, err := parseArgs(c.args, testDefaults())
		if err == nil || !isUsageError(err) {
			t.Fatalf("parseArgs(%v) = %v, want a usage error", c.args, err)
		}
		// The hint is a diagnosis, not a compatibility path: the option still
		// fails, and nothing was translated.
		if inv.cmd != cmdNone {
			t.Errorf("parseArgs(%v) produced %+v, want a failure and no invocation", c.args, inv)
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

// TestCredentialOptionsAreNeverAdvertised locks the refusal end to end at the
// parser boundary: the four ways of handing a secret to a command line are not
// part of this CLI, and the refusal must not pretend otherwise.
//
// These options were never part of this CLI, so a migration hint would tell a
// user that a capability was removed when it was never there. That is why the
// hint table must stay clear of them, and why this test asserts the absence of a
// hint rather than merely the failure.
//
// The value must not come back either: the refusal names the option, never what
// was offered as its value — a credential must not reach stderr, a log or an
// audit file.
func TestCredentialOptionsAreNeverAdvertised(t *testing.T) {
	const secret = "hunter2"

	for _, args := range [][]string{
		{"--password", secret},
		{"--password=" + secret},
		{"--password-stdin"},
		{"--token-stdin"},
		{"--non-interactive"},
	} {
		_, err := parseArgs(args, testDefaults())
		if err == nil || !isUsageError(err) {
			t.Fatalf("parseArgs(%v) = %v, want a usage error", args, err)
		}
		message := err.Error()
		if !strings.Contains(message, "unknown option") {
			t.Errorf("parseArgs(%v) error = %v, want an unknown option", args, message)
		}
		if strings.Contains(message, "hint:") {
			t.Errorf("parseArgs(%v) error = %v, want no migration hint: these options are new", args, message)
		}
		if strings.Contains(message, secret) {
			t.Errorf("parseArgs(%v) error = %v, want the supplied value withheld", args, message)
		}
	}

	// Structural guard: the hint table is for options that were removed, so a
	// credential option must never enter it — that would fabricate a history.
	for _, name := range []string{"password", "password-stdin", "token-stdin", "non-interactive"} {
		if _, ok := removedOptions[name]; ok {
			t.Errorf("removedOptions[%q] is set: this CLI never offered these, so they were never removed", name)
		}
	}
}

// TestParseLoginAndFilterFlags locks the auth command's inputs and the listing
// filters.
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

// TestParseClearAuth locks the local clear command and the grammar that replaced
// the old conflict checks: one command per line, so there is nothing to
// conflict with.
func TestParseClearAuth(t *testing.T) {
	if inv := parseOpts(t, "auth", "clear"); inv.cmd != cmdAuthClear {
		t.Errorf("auth clear = cmd %d", inv.cmd)
	}
	for _, args := range [][]string{
		{"auth", "clear", "--login"}, // the removed option
		{"auth", "clear", "login"},   // a second verb is an argument
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
	if inv.cfg.Threads != 8 || inv.cfg.Directories.Directory != filepath.Clean("/games") {
		t.Errorf("equals form: threads=%d directory=%q", inv.cfg.Threads, inv.cfg.Directories.Directory)
	}
	if inv.cfg.DownloadConfig.GalaxyPlatform != config.PlatformWindows {
		t.Error("--platform=windows did not select windows")
	}
}

// TestParseGalaxyCommands locks the galaxy family and the target split it carries.
func TestParseGalaxyCommands(t *testing.T) {
	if inv := parseOpts(t, "galaxy", "builds", "123"); inv.cmd != cmdGalaxyBuilds || inv.target.Build != "" {
		t.Errorf("galaxy builds = cmd %d target %+v", inv.cmd, inv.target)
	}
	manifest := parseOpts(t, "galaxy", "manifest", "123/2")
	if manifest.cmd != cmdGalaxyManifest || manifest.target.Product != "123" || manifest.target.Build != "2" {
		t.Errorf("galaxy manifest = cmd %d target %+v", manifest.cmd, manifest.target)
	}
	manifest2 := parseOpts(t, "galaxy", "manifest", "123", "2")
	if manifest2.cmd != cmdGalaxyManifest || manifest2.target.Product != "123" || manifest2.target.Build != "2" {
		t.Errorf("galaxy manifest 123 2 = cmd %d target %+v", manifest2.cmd, manifest2.target)
	}
	if inv := parseOpts(t, "galaxy", "cdns", "123"); inv.cmd != cmdGalaxyCDNs {
		t.Errorf("galaxy cdns = cmd %d", inv.cmd)
	}
	// "galaxy builds" lists builds; a build in the argument is a different
	// command, not a filter.
	if _, err := parseArgs([]string{"galaxy", "builds", "123/2"}, testDefaults()); err == nil {
		t.Error("galaxy builds with a build must be refused")
	}
}

// TestParseTargetErrors locks the target's shape rules.
func TestParseTargetErrors(t *testing.T) {
	for _, arg := range []string{"", "/2", "1/2/3", "/"} {
		if _, err := parseArgs([]string{"install", arg}, testDefaults()); err == nil {
			t.Errorf("install %q must be refused", arg)
		}
	}
	// An empty build is "no build given".
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

// TestNormalizeDirectory locks the directory normalisation invariants.
func TestNormalizeDirectory(t *testing.T) {
	// Platform-independent invariants
	if got, want := normalizeDirectory("", "."), filepath.Clean("."); got != want {
		t.Errorf(`normalizeDirectory("", ".") = %q, want %q`, got, want)
	}
	if got, want := normalizeDirectory("", "/fallback"), filepath.Clean("/fallback"); got != want {
		t.Errorf(`normalizeDirectory("", "/fallback") = %q, want %q`, got, want)
	}
	if got, want := normalizeDirectory("./foo/", "."), filepath.Clean("foo"); got != want {
		t.Errorf(`normalizeDirectory("./foo/", ".") = %q, want %q`, got, want)
	}

	// Windows-specific invariants (verified on Windows runtime)
	if runtime.GOOS == "windows" {
		if got, want := normalizeDirectory(`D:\Games\`, "."), `D:\Games`; got != want {
			t.Errorf(`normalizeDirectory("D:\\Games\\", ".") = %q, want %q`, got, want)
		}
		if got, want := normalizeDirectory(`D:/Games/`, "."), `D:\Games`; got != want {
			t.Errorf(`normalizeDirectory("D:/Games/", ".") = %q, want %q`, got, want)
		}
		// UNC paths with backslashes and mixed slashes
		if got, want := normalizeDirectory(`\\server\share\games\`, "."), `\\server\share\games`; got != want {
			t.Errorf(`normalizeDirectory("\\\\server\\share\\games\\", ".") = %q, want %q`, got, want)
		}
		if got, want := normalizeDirectory(`//server/share/games/`, "."), `\\server\share\games`; got != want {
			t.Errorf(`normalizeDirectory("//server/share/games/", ".") = %q, want %q`, got, want)
		}
		// Drive root
		if got, want := normalizeDirectory(`C:\`, "."), `C:\`; got != want {
			t.Errorf(`normalizeDirectory("C:\\", ".") = %q, want %q`, got, want)
		}
	}
}

// TestCapabilityWhitelistEnforcement locks that unaccepted capabilities are rejected per node.
func TestCapabilityWhitelistEnforcement(t *testing.T) {
	// auth clear accepts only Common Capability; network and transfer flags are rejected.
	if _, err := parseArgs([]string{"auth", "clear", "--retries", "5"}, testDefaults()); err == nil {
		t.Error("auth clear must not accept --retries")
	}
	if _, err := parseArgs([]string{"auth", "clear", "--threads", "4"}, testDefaults()); err == nil {
		t.Error("auth clear must not accept --threads")
	}

	// list tags accepts Common + Network + --json, but not Transfer/UI flags.
	if _, err := parseArgs([]string{"list", "tags", "--threads", "4"}, testDefaults()); err == nil {
		t.Error("list tags must not accept --threads")
	}
	if _, err := parseArgs([]string{"list", "tags", "--no-color"}, testDefaults()); err == nil {
		t.Error("list tags must not accept --no-color")
	}
	inv := parseOpts(t, "list", "tags", "--retries", "5", "--timeout", "10", "--json")
	if inv.cfg.Retries != 5 || inv.cfg.Curl.Timeout != 10 || !inv.json {
		t.Errorf("list tags accepted options mismatch: retries=%d timeout=%d json=%v",
			inv.cfg.Retries, inv.cfg.Curl.Timeout, inv.json)
	}
}

// TestParseThreadsAndProgressInterval locks the two numeric rendering/transfer
// knobs the parser owns, the interval's clamp included.
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
	// A legal but out-of-range interval is clamped, not replaced by the
	// default: the user asked for a cadence, just not one that is usable.
	if got := parseOpts(t, "install", "123", "--progress-interval", "99999").cfg.ProgressInterval; got != progressIntervalMax {
		t.Errorf("progress interval = %d, want the clamp to %d", got, progressIntervalMax)
	}
	if got := parseOpts(t, "install", "123", "--progress-interval", "0").cfg.ProgressInterval; got != progressIntervalMin {
		t.Errorf("progress interval = %d, want the clamp to %d", got, progressIntervalMin)
	}
}

// TestParseOrphansOptions locks the orphan command's options: the two filter
// files are its own, and the include mask is not — the walk checks everything.
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

// TestParseInstallDirTemplates locks the install root's template family: every
// template the resolver understands is accepted whole, and anything else
// carrying a "%" is refused — a half-exposed template language would reach the
// resolver unexpanded. A concrete directory name is TestParseInstallFlags'
// case.
func TestParseInstallDirTemplates(t *testing.T) {
	for _, template := range core.InstallSubdirTemplates {
		inv := parseOpts(t, "install", "123", "--install-dir", template)
		if inv.cfg.Directories.GalaxyInstallSubdir != template {
			t.Errorf("--install-dir %s = %q, want the template stored", template, inv.cfg.Directories.GalaxyInstallSubdir)
		}
	}
	for _, name := range []string{"%foo%", "%gamename%/data", "%install_dir%x", "a%b"} {
		err := mustUsageError(t, "install", "123", "--install-dir", name)
		if !strings.Contains(err.Error(), "known templates") {
			t.Errorf("--install-dir %s: error = %v, want the whitelist refusal", name, err)
		}
	}
}
