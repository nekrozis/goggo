package cli

import (
	"strings"
	"testing"

	"github.com/nekrozis/goggo/internal/config"
	"github.com/nekrozis/goggo/internal/util"
)

// baseConfig is the starting configuration the parser lays a command line on.
// A zero value is deliberate: the parser owns the defaults it declares, so a
// test can tell "the parser set this" from "the caller did".
func baseConfig() config.Config { return config.Config{} }

// mustParse fails the test when the line does not parse.
func mustParse(t *testing.T, args ...string) invocation {
	t.Helper()
	inv, err := parseArgs(args, baseConfig())
	if err != nil {
		t.Fatalf("parseArgs(%v): %v", args, err)
	}
	return inv
}

// mustUsageError fails the test unless the line fails as a usage error.
func mustUsageError(t *testing.T, args ...string) error {
	t.Helper()
	_, err := parseArgs(args, baseConfig())
	if err == nil {
		t.Fatalf("parseArgs(%v) succeeded, want a usage error", args)
	}
	if !isUsageError(err) {
		t.Fatalf("parseArgs(%v) error = %v, want a usage error (exit 2)", args, err)
	}
	return err
}

// TestCommandTreeResolution locks the tree itself (review S1 checkpoint 1): every
// supported path resolves to its own command, and the namespaces refuse to run
// on their own.
func TestCommandTreeResolution(t *testing.T) {
	cases := []struct {
		args []string
		want commandID
	}{
		{[]string{"auth", "login"}, cmdAuthLogin},
		{[]string{"auth", "logout"}, cmdAuthLogout},
		{[]string{"auth", "status"}, cmdAuthStatus},
		{[]string{"list", "games"}, cmdListGames},
		{[]string{"list", "tags"}, cmdListTags},
		{[]string{"list", "wishlist"}, cmdListWishlist},
		{[]string{"show", "builds", "123"}, cmdShowBuilds},
		{[]string{"show", "manifest", "123/2"}, cmdShowManifest},
		{[]string{"show", "cdns", "123"}, cmdShowCDNs},
		{[]string{"install", "123"}, cmdInstall},
		{[]string{"verify", "123"}, cmdVerify},
		{[]string{"orphans", "check", "123"}, cmdOrphansCheck},
		{[]string{"orphans", "remove", "123"}, cmdOrphansRemove},
	}
	for _, tc := range cases {
		if got := mustParse(t, tc.args...).cmd; got != tc.want {
			t.Errorf("parseArgs(%v).cmd = %d, want %d", tc.args, got, tc.want)
		}
	}

	// A namespace is not runnable, and a wrong subcommand is as unknown as a
	// wrong verb.
	for _, args := range [][]string{
		{"auth"}, {"list"}, {"show"}, {"orphans"},
		{"auth", "bogus"}, {"list", "bogus"}, {"orphans", "bogus"},
	} {
		err := mustUsageError(t, args...)
		if !strings.Contains(err.Error(), "subcommand") {
			t.Errorf("parseArgs(%v) error = %v, want it to name the subcommand problem", args, err)
		}
	}
}

// TestRemovedCommandsAreUnknown locks D14: the commands this build does not
// have are absent from the tree, and the ones whose capability moved somewhere
// else are diagnosed with a hint (review S1 checkpoint 6).
func TestRemovedCommandsAreUnknown(t *testing.T) {
	for _, name := range []string{"download", "repair", "xml", "cache", "cloud", "config"} {
		err := mustUsageError(t, name)
		if !strings.Contains(err.Error(), "unknown command") {
			t.Errorf("parseArgs(%s) error = %v, want an unknown command", name, err)
		}
	}
	// The hint is a diagnosis, not a compatibility path: the option still
	// fails, and nothing was translated.
	err := mustUsageError(t, "--galaxy-install", "123")
	if !strings.Contains(err.Error(), "unknown option") || !strings.Contains(err.Error(), "hint: use 'goggo install <game>'") {
		t.Errorf("error = %v, want the unknown option plus the migration hint", err)
	}
	inv, err2 := parseArgs([]string{"--galaxy-install", "123"}, baseConfig())
	if err2 == nil || inv.cmd != cmdNone {
		t.Errorf("removed option produced %+v / %v, want a failure and no invocation", inv, err2)
	}
}

// TestMetaShortcuts locks D18 (review S1 checkpoint 5): -h/--help and
// --version/version resolve as meta, carry the path they were asked about, and
// leave the business payload empty.
func TestMetaShortcuts(t *testing.T) {
	for _, args := range [][]string{{"-h"}, {"--help"}, {"help"}} {
		inv := mustParse(t, args...)
		if inv.meta != metaHelp || inv.cmd != cmdNone {
			t.Errorf("parseArgs(%v) = meta %d / cmd %d, want help with no command", args, inv.meta, inv.cmd)
		}
		if len(inv.helpPath) != 0 {
			t.Errorf("parseArgs(%v).helpPath = %v, want none", args, inv.helpPath)
		}
	}
	inv := mustParse(t, "install", "-h")
	if inv.meta != metaHelp || strings.Join(inv.helpPath, " ") != "install" {
		t.Errorf("install -h = meta %d / path %v, want help for install", inv.meta, inv.helpPath)
	}
	if inv.cmd != cmdNone || inv.target.Product != "" {
		t.Errorf("install -h carried a payload: %+v", inv)
	}
	inv = mustParse(t, "help", "orphans", "remove")
	if inv.meta != metaHelp || strings.Join(inv.helpPath, " ") != "orphans remove" {
		t.Errorf("help orphans remove = meta %d / path %v", inv.meta, inv.helpPath)
	}
	for _, args := range [][]string{{"--version"}, {"version"}} {
		if inv := mustParse(t, args...); inv.meta != metaVersion || inv.cmd != cmdNone {
			t.Errorf("parseArgs(%v) = meta %d / cmd %d, want version", args, inv.meta, inv.cmd)
		}
	}
	// Meta answers even when the rest of the line is incomplete.
	if inv := mustParse(t, "install", "-h"); inv.meta != metaHelp {
		t.Error("install -h must not fail on the missing game")
	}
}

// TestTypedPayload locks that the parser hands over semantics and nothing else
// (review S1 checkpoint 2): the target is split, the destructive flag is its own
// field, and the command decides the rest.
func TestTypedPayload(t *testing.T) {
	inv := mustParse(t, "install", "1207658787/1234")
	if inv.target.Product != "1207658787" || inv.target.Build != "1234" {
		t.Errorf("target = %+v, want product 1207658787 build 1234", inv.target)
	}
	if inv := mustParse(t, "verify", "1207658787"); inv.target.Product != "1207658787" || inv.target.Build != "" {
		t.Errorf("target = %+v, want a product with no build", inv.target)
	}
	if inv := mustParse(t, "orphans", "remove", "1207658787", "--yes"); !inv.yes {
		t.Error("orphans remove --yes did not set the confirmation flag")
	}
	if inv := mustParse(t, "orphans", "check", "1207658787"); inv.yes {
		t.Error("orphans check carried the destructive flag")
	}
	// A target that cannot be split into at most two parts is refused rather
	// than truncated.
	for _, args := range [][]string{
		{"install", "1/2/3"}, {"install", "/2"}, {"install", ""},
	} {
		mustUsageError(t, args...)
	}
}

// TestOptionAcceptanceIsPerCommand locks D15 (review S1 checkpoint 3): "shared"
// means shared semantics, not unconditional acceptance.
func TestOptionAcceptanceIsPerCommand(t *testing.T) {
	// Accepted where it means something.
	if inv := mustParse(t, "install", "123", "--threads", "8", "--check-free-space"); inv.cfg.Threads != 8 {
		t.Errorf("threads = %d, want 8", inv.cfg.Threads)
	}
	// The same option on a command that cannot use it fails instead of being
	// silently ignored.
	err := mustUsageError(t, "list", "games", "--threads", "8")
	if !strings.Contains(err.Error(), "--threads") || !strings.Contains(err.Error(), "list") {
		t.Errorf("error = %v, want it to name the option and the command", err)
	}
	// The orphan walk never narrows by include/exclude (upstream checks
	// everything), so the option is refused rather than accepted and ignored.
	mustUsageError(t, "orphans", "check", "123", "--include", "installers")
	mustUsageError(t, "orphans", "check", "123", "--tag", "x")
	// --yes belongs to the destructive command only (D16).
	mustUsageError(t, "install", "123", "--yes")
	mustUsageError(t, "orphans", "check", "123", "--yes")
	// verify honours the include mask and the blacklist (upstream --status).
	if inv := mustParse(t, "verify", "123", "--include", "installers"); inv.cfg.DownloadConfig.Include == 0 {
		t.Error("verify --include produced an empty mask")
	}
	// Filters belong to the listing they filter (D7).
	mustUsageError(t, "install", "123", "--tag", "x")
	if inv := mustParse(t, "list", "games", "--tag", "rpg,indie"); len(inv.cfg.DownloadConfig.Tags) != 2 {
		t.Errorf("tags = %v, want two entries", inv.cfg.DownloadConfig.Tags)
	}
}

// TestValueParsersReuseTheExistingSemantics locks checkpoint 4: the new parser
// must produce exactly what the shared option helpers produce, not a second
// interpretation of the same syntax.
func TestValueParsersReuseTheExistingSemantics(t *testing.T) {
	inv := mustParse(t, "list", "games", "--installer-platform", "windows,linux+mac")
	wantPriority, wantMask := util.ParseOptionString("windows,linux+mac", config.Platforms)
	if inv.cfg.DownloadConfig.InstallerPlatform != wantMask || inv.cfg.PlatformPriority != "windows,linux+mac" {
		t.Errorf("installer platform = %#x/%q, want %#x/%q",
			inv.cfg.DownloadConfig.InstallerPlatform, inv.cfg.PlatformPriority, wantMask, "windows,linux+mac")
	}
	if len(wantPriority) == 0 {
		t.Error("the shared helper produced no priority, so the test proves nothing")
	}
	if len(inv.cfg.DownloadConfig.PlatformPriority) != len(wantPriority) {
		t.Errorf("platform priority = %v, want %v", inv.cfg.DownloadConfig.PlatformPriority, wantPriority)
	}

	inv = mustParse(t, "verify", "123", "--include", "installers,patches", "--exclude", "patches")
	want := optionMask("installers,patches", config.IncludeOptions) &^ optionMask("patches", config.IncludeOptions)
	if inv.cfg.DownloadConfig.Include != want {
		t.Errorf("include mask = %#x, want %#x", inv.cfg.DownloadConfig.Include, want)
	}

	// --platform selects the Galaxy platform (single value), while the listing
	// uses --installer-platform (priority syntax): two different options, two
	// different meanings (the v5 collision, closed as B).
	inv = mustParse(t, "install", "123", "--platform", "windows")
	if inv.cfg.DownloadConfig.GalaxyPlatform != config.PlatformWindows {
		t.Errorf("galaxy platform = %#x, want windows", inv.cfg.DownloadConfig.GalaxyPlatform)
	}
	mustUsageError(t, "install", "123", "--platform", "bogus")
	mustUsageError(t, "list", "games", "--installer-platform", "bogus")

	// --arch keeps the upstream reading: an unmatched value, and "all", mean
	// 64-bit.
	if inv := mustParse(t, "install", "123", "--arch", "bogus"); inv.cfg.DownloadConfig.GalaxyArch != config.ArchX64 {
		t.Error("--arch bogus must fall back to x64, as upstream does")
	}

	// --progress-interval is clamped, not replaced by the default (FIX-2).
	if inv := mustParse(t, "install", "123", "--progress-interval", "99999"); inv.cfg.ProgressInterval != progressIntervalMax {
		t.Errorf("progress interval = %d, want the clamp to %d", inv.cfg.ProgressInterval, progressIntervalMax)
	}
	if inv := mustParse(t, "install", "123", "--progress-interval", "0"); inv.cfg.ProgressInterval != progressIntervalMin {
		t.Errorf("progress interval = %d, want the clamp to %d", inv.cfg.ProgressInterval, progressIntervalMin)
	}

	// --directory is normalised the way the install path is built.
	if inv := mustParse(t, "install", "123", "--directory", "/tmp/games"); inv.cfg.Directories.Directory != "/tmp/games/" {
		t.Errorf("directory = %q, want a trailing separator", inv.cfg.Directories.Directory)
	}
	if inv := mustParse(t, "install", "123"); inv.cfg.Directories.Directory != defaultDirectory {
		t.Errorf("default directory = %q, want %q", inv.cfg.Directories.Directory, defaultDirectory)
	}

	// --install-dir takes a directory name; the internal template language is
	// not part of the CLI contract (review §5, constraint A).
	if inv := mustParse(t, "install", "123", "--install-dir", "HoMM 3 Complete"); inv.cfg.Directories.GalaxyInstallSubdir != "HoMM 3 Complete" {
		t.Error("--install-dir did not store the given directory name")
	}
	err := mustUsageError(t, "install", "123", "--install-dir", "%install_dir%")
	if !strings.Contains(err.Error(), "not a template") {
		t.Errorf("error = %v, want the template refusal", err)
	}

	// --verbose selects the verbose level, and nothing else.
	if inv := mustParse(t, "list", "games", "--verbose"); inv.cfg.MsgLevel != msgLevelVerbose {
		t.Errorf("msg level = %d, want %d", inv.cfg.MsgLevel, msgLevelVerbose)
	}
	if inv := mustParse(t, "list", "games", "-v"); inv.cfg.MsgLevel != msgLevelVerbose {
		t.Errorf("-v did not select the verbose level")
	}

	// Negations clear their setting.
	if inv := mustParse(t, "install", "123", "--no-subdirectories"); inv.cfg.Directories.SubDirectories {
		t.Error("--no-subdirectories left the setting on")
	}
	if inv := mustParse(t, "install", "123", "--no-dependencies"); inv.cfg.DownloadConfig.GalaxyDependencies {
		t.Error("--no-dependencies left the setting on")
	}
}

// TestUnknownAndMalformedAreUsageErrors locks checkpoint 7: everything the
// parser refuses comes back as one error shape, so the caller has a single
// mapping onto the usage exit code.
func TestUnknownAndMalformedAreUsageErrors(t *testing.T) {
	for _, args := range [][]string{
		{"bogus"},
		{"--bogus"},
		{"install", "123", "--bogus"},
		{"install", "123", "--threads"},
		{"install"},
		{"install", "1", "2"},
		{"verify", "1", "2"},
		{"list", "games", "extra"},
		{"show", "builds", "1/2"},
		{"show", "builds"},
		{"auth", "login", "extra"},
		{"version", "extra"},
		{"install", "123", "--verbose=yes"},
	} {
		err := mustUsageError(t, args...)
		if !strings.Contains(err.Error(), "unknown") && !strings.Contains(err.Error(), "takes") &&
			!strings.Contains(err.Error(), "needs") && !strings.Contains(err.Error(), "requires") &&
			!strings.Contains(err.Error(), "invalid") && !strings.Contains(err.Error(), "not accepted") &&
			!strings.Contains(err.Error(), "does not take") {
			t.Errorf("parseArgs(%v) error = %v, want a recognisable usage failure", args, err)
		}
	}
}

// TestBareInvocationAndDefaults locks the two boundaries S1 owns: a line with no
// command parses to nothing to run (the caller decides what to print), and the
// parser installs its own defaults while leaving the thread count alone (D9).
func TestBareInvocationAndDefaults(t *testing.T) {
	inv := mustParse(t)
	if inv.cmd != cmdNone || inv.meta != metaNone {
		t.Errorf("bare invocation = cmd %d / meta %d, want nothing to run", inv.cmd, inv.meta)
	}
	inv = mustParse(t, "--verbose")
	if inv.cmd != cmdNone || inv.cfg.MsgLevel != msgLevelVerbose {
		t.Error("options without a command must still parse, with nothing to run")
	}

	cfg := baseConfig()
	if _, err := parseArgs([]string{"install", "123"}, cfg); err != nil {
		t.Fatal(err)
	}
	got := mustParse(t, "install", "123").cfg
	if got.GalaxyBuildSortingOrder != defaultGalaxyBuildSort || got.Directories.GalaxyInstallSubdir != defaultGalaxyInstallSubdir {
		t.Errorf("parser defaults missing: %+v", got.Directories)
	}
	if !got.Directories.SubDirectories || !got.DownloadConfig.GalaxyDependencies {
		t.Error("the negations' defaults must be the positive values")
	}
	if got.Threads != 0 {
		t.Errorf("threads default = %d: the worker count is settled by the benchmark (D9), not here", got.Threads)
	}
}

// TestOptionPrefixStrictness locks the option-name grammar (review S1-R1): a
// long option is "--name", a short one is "-x" for a declared single-letter
// alias, and nothing else is an option. Stripping every leading dash — the first
// cut's behaviour — accepted "---help", "-version" and "-threads 8", which is a
// wider grammar than the CLI has.
func TestOptionPrefixStrictness(t *testing.T) {
	// The legal shapes still work.
	if inv := mustParse(t, "-h"); inv.meta != metaHelp {
		t.Error("-h must be help")
	}
	if inv := mustParse(t, "list", "games", "-v"); inv.cfg.MsgLevel != msgLevelVerbose {
		t.Error("-v must be the verbose short option")
	}
	if inv := mustParse(t, "list", "games", "--verbose"); inv.cfg.MsgLevel != msgLevelVerbose {
		t.Error("--verbose must work")
	}

	// Everything else is refused, and refused as a usage error.
	for _, args := range [][]string{
		{"---help"},       // three dashes
		{"--h"},           // a short alias behind a long prefix
		{"-help"},         // a long name behind a short prefix
		{"-version"},      // the long name spelled short
		{"-threads", "8"}, // a long name with a value
		{"-verbose"},      // an alias behind the wrong prefix
		{"-v=1"},          // a short option does not take a value
		{"--"},            // no end-of-options marker
		{"install", "1", "-threads", "8"},
	} {
		err := mustUsageError(t, args...)
		if !strings.Contains(err.Error(), "unknown") && !strings.Contains(err.Error(), "unexpected") {
			t.Errorf("parseArgs(%v) error = %v, want an unknown/unexpected failure", args, err)
		}
	}
}

// TestOptionTableIsComplete locks a structural property the first cut of the
// table got wrong: an option that parses but has no setter is an option that
// silently does nothing (--no-color and --no-unicode were exactly that). Every
// entry must either carry a setter or be one the parser handles itself.
func TestOptionTableIsComplete(t *testing.T) {
	handledByTheParser := map[optionID]bool{
		optHelp:    true, // meta, answered after the tree is known
		optVersion: true, // meta
		optInclude: true, // combined with exclude after the loop
		optExclude: true,
	}
	for i := range optionTable {
		spec := &optionTable[i]
		if spec.parse == nil && !handledByTheParser[spec.id] {
			t.Errorf("option --%s has no setter and is not handled by the parser", spec.long)
		}
	}
}

// TestMigrationHintsAreUsable locks a property the first cut of the hint table
// got wrong twice over: a hint is only useful if the user can paste it. Every
// hint must therefore name a command the tree actually has — and, when it names
// an option, one that command actually accepts (review S2: "goggo list
// --installer-platform" named a command path that does not exist, because the
// installer filters belong to "list games").
func TestMigrationHintsAreUsable(t *testing.T) {
	for flag, hint := range removedOptions {
		if hint == "" {
			t.Errorf("removed option --%s has no hint and no reason to be listed", flag)
			continue
		}
		fields := strings.Fields(hint)
		if len(fields) < 2 || fields[0] != "goggo" {
			t.Errorf("hint for --%s = %q, want it to start with the program name", flag, hint)
			continue
		}

		var (
			path    []string
			optName string
		)
		for _, field := range fields[1:] {
			switch {
			case strings.HasPrefix(field, "-"):
				optName = strings.TrimLeft(field, "-")
			case strings.HasPrefix(field, "<"), strings.HasPrefix(field, "("):
				// A placeholder or a parenthetical ends the command path.
			default:
				path = append(path, field)
			}
			if optName != "" || strings.HasPrefix(field, "<") || strings.HasPrefix(field, "(") {
				break
			}
		}

		var node commandNode
		if len(path) != 0 {
			resolved, _, rest, err := resolveCommand(path)
			if err != nil || resolved.name == "" || len(rest) != 0 {
				t.Errorf("hint for --%s = %q: %v names no runnable command", flag, hint, path)
				continue
			}
			node = resolved
		}
		if optName == "" {
			continue
		}
		spec := longByName[optName]
		if spec == nil {
			spec = shortByName[optName]
		}
		if spec == nil {
			t.Errorf("hint for --%s = %q names option --%s, which does not exist", flag, hint, optName)
			continue
		}
		if len(path) != 0 && !node.accepts(spec.id) {
			t.Errorf("hint for --%s = %q: %v does not accept --%s", flag, hint, path, optName)
		}
	}
}

// TestHelpTopicResolution locks the ONE topic rule both spellings share (review
// S3): -h/--help and the help command resolve the same path the same way, and
// an unknown or over-specified topic is a usage failure rather than a silent
// root help.
func TestHelpTopicResolution(t *testing.T) {
	ok := []struct {
		args []string
		path string
	}{
		{[]string{"-h"}, ""},
		{[]string{"--help"}, ""},
		{[]string{"help"}, ""},
		{[]string{"install", "-h"}, "install"},
		{[]string{"install", "--help", "123"}, "install"}, // help does not require the game
		{[]string{"help", "install"}, "install"},
		{[]string{"help", "auth", "login"}, "auth login"},
		{[]string{"auth", "-h"}, "auth"}, // a namespace has a topic too
		{[]string{"help", "orphans"}, "orphans"},
		{[]string{"help", "version"}, "version"},
	}
	for _, tc := range ok {
		inv := mustParse(t, tc.args...)
		if inv.meta != metaHelp || strings.Join(inv.helpPath, " ") != tc.path {
			t.Errorf("parseArgs(%v) = meta %d path %v, want help %q", tc.args, inv.meta, inv.helpPath, tc.path)
		}
	}

	bad := [][]string{
		{"help", "bogus"},
		{"help", "auth", "bogus"},
		{"--help", "bogus"},
		{"install", "-h", "1", "2"},
		{"list", "games", "-h", "extra"}, // no argument to spend
	}
	for _, args := range bad {
		err := mustUsageError(t, args...)
		if !strings.Contains(err.Error(), "unknown") && !strings.Contains(err.Error(), "takes") {
			t.Errorf("parseArgs(%v) error = %v, want an unknown command or an arity failure", args, err)
		}
	}
}
