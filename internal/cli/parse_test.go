package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
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

// TestCommandTreeResolution locks the tree itself: every
// supported path resolves to its own command, and the namespaces refuse to run
// on their own.
func TestCommandTreeResolution(t *testing.T) {
	cases := []struct {
		args []string
		want commandID
	}{
		{[]string{"auth", "login"}, cmdAuthLogin},
		{[]string{"auth", "clear"}, cmdAuthClear},
		{[]string{"auth", "status"}, cmdAuthStatus},
		{[]string{"list", "games"}, cmdListGames},
		{[]string{"list", "tags"}, cmdListTags},
		{[]string{"list", "wishlist"}, cmdListWishlist},
		{[]string{"game", "123"}, cmdGame},
		{[]string{"galaxy", "builds", "123"}, cmdGalaxyBuilds},
		{[]string{"galaxy", "manifest", "123/2"}, cmdGalaxyManifest},
		{[]string{"galaxy", "manifest", "123", "2"}, cmdGalaxyManifest},
		{[]string{"galaxy", "cdns", "123"}, cmdGalaxyCDNs},
		{[]string{"galaxy", "cdns", "123", "2"}, cmdGalaxyCDNs},
		{[]string{"install", "123"}, cmdInstall},
		{[]string{"install", "options", "123"}, cmdInstallOptions},
		{[]string{"verify", "123"}, cmdVerify},
		{[]string{"backup", "list"}, cmdBackupList},
		{[]string{"backup", "list", "123"}, cmdBackupList},
		{[]string{"backup", "download", "123"}, cmdBackupDownload},
		{[]string{"backup", "download", "123", "file1"}, cmdBackupDownload},
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
		{"auth"}, {"list"}, {"galaxy"}, {"orphans"}, {"backup"},
		{"auth", "bogus"}, {"list", "bogus"}, {"galaxy", "bogus"}, {"orphans", "bogus"}, {"backup", "bogus"},
	} {
		err := mustUsageError(t, args...)
		if !strings.Contains(err.Error(), "subcommand") {
			t.Errorf("parseArgs(%v) error = %v, want it to name the subcommand problem", args, err)
		}
	}
}

// TestRemovedCommandsAreUnknown locks the product surface: the commands this
// build does not have are absent from the tree. The removed option whose
// capability moved somewhere else is the parser's own case in options_test.go
// (TestParseUnknownAndRemovedOptions).
func TestRemovedCommandsAreUnknown(t *testing.T) {
	for _, name := range []string{"repair", "xml", "cache", "cloud", "config"} {
		err := mustUsageError(t, name)
		if !strings.Contains(err.Error(), "unknown command") {
			t.Errorf("parseArgs(%s) error = %v, want an unknown command", name, err)
		}
	}
}

// TestMetaShortcuts locks the meta shortcuts: -h/--help and --version/version
// resolve as meta, carry the path they were asked about, and leave the business
// payload empty.
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
// : the target is split, the destructive flag is its own
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

// TestOptionAcceptanceIsPerCommand locks what "shared" means: shared semantics,
// not unconditional acceptance.
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
	// The orphan walk never narrows by include/exclude — it checks everything —
	// so the option is refused rather than accepted and ignored.
	mustUsageError(t, "orphans", "check", "123", "--include", "installers")
	mustUsageError(t, "orphans", "check", "123", "--tag", "x")
	// --yes belongs to the destructive command only.
	mustUsageError(t, "install", "123", "--yes")
	mustUsageError(t, "orphans", "check", "123", "--yes")
	// verify honours the include mask and the blacklist.
	if inv := mustParse(t, "verify", "123", "--include", "installers"); inv.cfg.DownloadConfig.Include == 0 {
		t.Error("verify --include produced an empty mask")
	}
	// Filters belong to the listing they filter.
	mustUsageError(t, "install", "123", "--tag", "x")
	if inv := mustParse(t, "list", "games", "--tag", "rpg,indie"); len(inv.cfg.DownloadConfig.Tags) != 2 {
		t.Errorf("tags = %v, want two entries", inv.cfg.DownloadConfig.Tags)
	}
}

// TestInstallerPlatformReusesTheSharedOptionHelper locks the listing side of
// the platform vocabulary (the install side is options_test.go's
// TestParsePlatformAndLanguage): --installer-platform speaks the shared
// priority syntax, and the parser produces exactly what the shared helper
// produces for it. A second interpretation of the same syntax is what this
// refuses.
func TestInstallerPlatformReusesTheSharedOptionHelper(t *testing.T) {
	const list = "windows,linux+mac"
	wantPriority, wantMask := util.ParseOptionString(list, config.Platforms)
	// The helper's own answer is stated here so a helper that stopped grouping
	// would not quietly turn the comparison below into a tautology: one group
	// per comma, each group the OR of its "+"-joined parts.
	wantGrouped := config.PlatformLinux | config.PlatformMac
	if len(wantPriority) != 2 || wantPriority[0] != config.PlatformWindows || wantPriority[1] != wantGrouped ||
		wantMask != config.PlatformWindows|wantGrouped {
		t.Fatalf("ParseOptionString(%q) = %v/%#x, want the windows group then the linux+mac group", list, wantPriority, wantMask)
	}

	inv := mustParse(t, "list", "games", "--installer-platform", list)
	if inv.cfg.DownloadConfig.InstallerPlatform != wantMask {
		t.Errorf("installer platform = %#x, want the helper's %#x", inv.cfg.DownloadConfig.InstallerPlatform, wantMask)
	}
	if inv.cfg.PlatformPriority != list {
		t.Errorf("platform priority = %q, want the list kept verbatim", inv.cfg.PlatformPriority)
	}
	if got := inv.cfg.DownloadConfig.PlatformPriority; len(got) != len(wantPriority) ||
		got[0] != wantPriority[0] || got[1] != wantPriority[1] {
		t.Errorf("platform priority groups = %v, want the helper's %v", got, wantPriority)
	}

	// An unmatched value is that same helper's failure: it resolves to no bit,
	// which the parser refuses instead of storing.
	mustUsageError(t, "list", "games", "--installer-platform", "bogus")
}

// TestUnknownAndMalformedAreUsageErrors locks the single failure class:
// everything the parser refuses — an unknown word, a missing value, a bad
// arity, a value the option cannot take — comes back as a usage error, which is
// the one mapping the caller has onto exit code 2. The wording is not the
// contract here; run_test.go's TestRunFailures drives the same class through
// Run and onto the code.
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
		{"galaxy", "builds", "1/2"},
		{"galaxy", "builds"},
		{"galaxy", "manifest"},
		{"auth", "login", "extra"},
		{"version", "extra"},
		{"install", "123", "--verbose=yes"},
	} {
		mustUsageError(t, args...)
	}
}

// TestDeprecatedCommandsProvideActionableHints locks the migration ergonomics:
// former commands from CLI 1 produce actionable hints pointing directly to their
// CLI 2 replacements, while unknown commands that merely share a prefix do not.
func TestDeprecatedCommandsProvideActionableHints(t *testing.T) {
	exact := []struct {
		args []string
		want string
	}{
		{[]string{"show", "builds", "123"}, "goggo galaxy builds"},
		{[]string{"show", "manifest", "123"}, "goggo galaxy manifest"},
		{[]string{"show", "cdns", "123"}, "goggo galaxy cdns"},
		{[]string{"download", "123"}, "goggo backup download"},
		{[]string{"download", "file", "123", "f1"}, "goggo backup download"},
		{[]string{"list", "details"}, "goggo backup list"},
		{[]string{"list", "json"}, "--json"},
		{[]string{"auth", "logout"}, "goggo auth clear"},
	}
	for _, tc := range exact {
		err := mustUsageError(t, tc.args...)
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("parseArgs(%v) = %v, want hint mentioning %q", tc.args, err, tc.want)
		}
	}

	// Help topics on deprecated commands also yield actionable hints.
	for _, topic := range []struct {
		args []string
		want string
	}{
		{[]string{"help", "show"}, "galaxy"},
		{[]string{"help", "download"}, "backup download"},
		{[]string{"help", "list", "details"}, "backup list"},
		{[]string{"help", "list", "json"}, "--json"},
		{[]string{"help", "auth", "logout"}, "auth clear"},
	} {
		err := mustUsageError(t, topic.args...)
		if !strings.Contains(err.Error(), topic.want) {
			t.Errorf("parseArgs(%v) = %v, want hint mentioning %q", topic.args, err, topic.want)
		}
	}

	// Negative boundary tests: words that merely share a prefix must not trigger deprecation hints.
	negative := [][]string{
		{"showfoo"},
		{"downloadable"},
		{"list", "detail"},
		{"auth", "logoutx"},
	}
	for _, args := range negative {
		err := mustUsageError(t, args...)
		msg := err.Error()
		if strings.Contains(msg, "goggo galaxy") || strings.Contains(msg, "goggo backup") || strings.Contains(msg, "goggo auth clear") {
			t.Errorf("parseArgs(%v) = %v, must not produce a replacement hint", args, err)
		}
	}
}

// TestBareInvocation locks the boundary a line with no command leaves: nothing
// to run (the caller decides what to print), and options without a command
// still parse. The defaults the parser installs are locked in
// TestThreadsPrecedence below, options_test.go's TestParseInstallDefaults and
// subdir_test.go.
func TestBareInvocation(t *testing.T) {
	inv := mustParse(t)
	if inv.cmd != cmdNone || inv.meta != metaNone {
		t.Errorf("bare invocation = cmd %d / meta %d, want nothing to run", inv.cmd, inv.meta)
	}
	inv = mustParse(t, "--verbose")
	if inv.cmd != cmdNone || inv.cfg.MsgLevel != msgLevelVerbose {
		t.Error("options without a command must still parse, with nothing to run")
	}
}

// TestThreadsPrecedence locks the three-way precedence the default introduces
// : the parser's measured default, an explicit count that wins over
// it, and an explicit 0 that keeps its own meaning. 0 must NOT become a second
// spelling of the default — it is the request for the runtime's single-worker
// fallback, which lives in transfer/schedule and is not the CLI's to redefine.
func TestThreadsPrecedence(t *testing.T) {
	if got := mustParse(t, "install", "123").cfg.Threads; got != defaultThreads {
		t.Errorf("absent --threads: threads = %d, want the default %d", got, defaultThreads)
	}
	if got := mustParse(t, "install", "123", "--threads", "6").cfg.Threads; got != 6 {
		t.Errorf("--threads 6: threads = %d, want 6", got)
	}
	if got := mustParse(t, "install", "123", "--threads", "0").cfg.Threads; got != 0 {
		t.Errorf("--threads 0: threads = %d, want 0 (the runtime fallback, not the default)", got)
	}
}

// TestOptionPrefixStrictness locks the option-name grammar: a
// long option is "--name", a short one is "-x" for a declared single-letter
// alias, and nothing else is an option. Stripping every leading dash would
// accept "---help", "-version" and "-threads 8", which is a wider grammar than
// the CLI has.
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

// TestOptionTableIsComplete locks a structural property: an option that parses
// but has no setter is an option that silently does nothing. Every entry must
// either carry a setter or be one the parser handles itself.
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

// TestMigrationHintsAreUsable locks the property that makes a hint useful: the
// user must be able to paste it. Every hint therefore names a command the tree
// actually has — and, when it names an option, one that command actually
// accepts.
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

// TestHelpTopicResolution locks the ONE topic rule both spellings share:
// -h/--help and the help command resolve the same path the same way, and an
// unknown or over-specified topic is a usage failure rather than a silent root
// help.
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
		{[]string{"help", "auth", "login"}, "auth login"},
		{[]string{"auth", "-h"}, "auth"}, // a namespace has a topic too
		{[]string{"help", "galaxy"}, "galaxy"},
		{[]string{"help", "galaxy", "builds"}, "galaxy builds"},
		{[]string{"help", "backup"}, "backup"},
		{[]string{"help", "backup", "list"}, "backup list"},
		{[]string{"help", "backup", "download"}, "backup download"},
		{[]string{"help", "install", "options"}, "install options"},
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

// TestChunkSizeOptionValidation locks the --chunk-size contract: the unit is
// MiB and the value must be a positive integer — zero, negative and non-numeric
// are usage errors, not silent defaults.
func TestChunkSizeOptionValidation(t *testing.T) {
	inv := mustParse(t, "manifest", "create", "f.bin", "--chunk-size", "10")
	if got := inv.cfg.DownloadConfig.ChunkSize; got != 10*1024*1024 {
		t.Errorf("--chunk-size 10 = %d bytes, want %d", got, 10*1024*1024)
	}
	for _, bad := range []string{"0", "-1", "abc", "1.5", "1025", "8796093022208"} {
		mustUsageError(t, "manifest", "create", "f.bin", "--chunk-size", bad)
	}
	if inv := mustParse(t, "manifest", "create", "f.bin", "--chunk-size", "1024"); inv.cfg.DownloadConfig.ChunkSize != 1024*1024*1024 {
		t.Errorf("--chunk-size 1024 = %d bytes, want 2^30", inv.cfg.DownloadConfig.ChunkSize)
	}
}

// failingWriter answers every write with an error, standing in for a closed or
// broken stdout.
type failingWriter struct{}

func (failingWriter) Write(p []byte) (int, error) { return 0, errors.New("broken pipe") }

// shortWriter reports success for all but the final byte — the shape a partial
// write takes, which a bare err check would miss.
type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) { return len(p) - 1, nil }

// TestManifestCreateStdoutFailure locks the create contract on the `-o -`
// path: a stdout write failure — error or short write — is exit 1, not a
// silent success with a half-emitted manifest.
func TestManifestCreateStdoutFailure(t *testing.T) {
	isolateRoots(t)
	dir := t.TempDir()
	target := filepath.Join(dir, "small.bin")
	patternFile(t, target, 4096)
	cfg, err := newConfig()
	if err != nil {
		t.Fatal(err)
	}

	for name, w := range map[string]io.Writer{"error": failingWriter{}, "short write": shortWriter{}} {
		inv := invocation{cfg: cfg, outputFile: "-"}
		inv.target.Product = target
		var errOut bytes.Buffer
		if got := runManifestCreate(inv, w, &errOut); got != outcomeOperationFailure {
			t.Errorf("%s: outcome = %v, want an operation failure", name, got)
		}
	}
}

// TestOpenKindClassification unit-tests the classifier itself: the sentinel and
// its wraps go to MISSING_*, everything else (EACCES, EIO, ELOOP, …) is
// IO_ERROR. The integration cases below keep only the exit-code assertion
// because the platform error surfaces differ: on Windows an ENOTDIR path is
// reported as path-not-exist, which makes MISSING_* the correct answer there.
func TestOpenKindClassification(t *testing.T) {
	cases := []struct {
		err     error
		missing string
		want    string
	}{
		{os.ErrNotExist, "MISSING_FILE", "MISSING_FILE"},
		{fmt.Errorf("stat: %w", fs.ErrNotExist), "MISSING_MANIFEST", "MISSING_MANIFEST"},
		{errors.New("permission denied"), "MISSING_FILE", "IO_ERROR"},
		{&os.PathError{Op: "open", Path: "p", Err: errors.New("EIO")}, "MISSING_MANIFEST", "IO_ERROR"},
	}
	for _, c := range cases {
		if got := openKind(c.err, c.missing); got != c.want {
			t.Errorf("openKind(%v, %s) = %s, want %s", c.err, c.missing, got, c.want)
		}
	}
}

// TestManifestIOErrorClassification locks the wiring end to end: an unopenable
// input is an ERROR response with exit 2 on every path that reads a file,
// whatever the platform's exact kind label for this shape.
func TestManifestIOErrorClassification(t *testing.T) {
	dir := t.TempDir()
	reg := filepath.Join(dir, "reg.bin")
	if err := os.WriteFile(reg, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "ok.bin")
	patternFile(t, target, 2048)

	cases := []struct {
		name string
		args []string
	}{
		{"target under a regular file", []string{"manifest", "verify", filepath.Join(reg, "child.bin"), "--json"}},
		{"explicit --xml under a regular file", []string{"manifest", "verify", target, "--xml", filepath.Join(reg, "m.xml"), "--json"}},
		{"inspect under a regular file", []string{"manifest", "inspect", filepath.Join(reg, "m.xml"), "--json"}},
	}
	for _, c := range cases {
		code, out, _ := runManifestCLI(t, c.args...)
		if code != 2 {
			t.Errorf("%s: exit = %d, want 2 (out=%s)", c.name, code, out)
		}
		var doc map[string]any
		if err := json.Unmarshal([]byte(out), &doc); err != nil {
			t.Errorf("%s: not JSON: %s", c.name, out)
			continue
		}
		if doc["status"] != "ERROR" {
			t.Errorf("%s: status = %v, want ERROR", c.name, doc["status"])
		}
	}
}
