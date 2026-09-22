package cli

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/nekrozis/goggo/internal/config"
	"github.com/nekrozis/goggo/internal/core"
	"github.com/nekrozis/goggo/internal/gamedetails"
)

// TestEndToEndCommandTopology verifies:
//  1. Exactly 17 runnable commands exist in the command topology (16 terminal leaves + 1 dual node "install").
//  2. All registered runnable commands have valid non-zero IDs, unique paths, and declared session classes.
//  3. Pure namespaces have zero command IDs (cmdNone) and never masquerade as runnable commands.
//  4. No deprecated command path (show, download, list details, list json, auth logout)
//     exists in the production commandTree.
func TestEndToEndCommandTopology(t *testing.T) {
	deprecatedPaths := map[string]bool{
		"show":         true,
		"download":     true,
		"list details": true,
		"list json":    true,
		"auth logout":  true,
	}

	expectedCommands := map[string]commandID{
		"auth login":      cmdAuthLogin,
		"auth clear":      cmdAuthClear,
		"auth status":     cmdAuthStatus,
		"list games":      cmdListGames,
		"list tags":       cmdListTags,
		"list wishlist":   cmdListWishlist,
		"game":            cmdGame,
		"galaxy builds":   cmdGalaxyBuilds,
		"galaxy manifest": cmdGalaxyManifest,
		"galaxy cdns":     cmdGalaxyCDNs,
		"install":         cmdInstall,
		"install options": cmdInstallOptions,
		"verify":          cmdVerify,
		"backup list":     cmdBackupList,
		"backup download": cmdBackupDownload,
		"orphans check":   cmdOrphansCheck,
		"orphans remove":  cmdOrphansRemove,
	}

	var leafCount int
	var dualCount int
	foundCommands := make(map[string]commandID)

	var walk func(path []string, nodes []commandNode)
	walk = func(path []string, nodes []commandNode) {
		for _, n := range nodes {
			fullPath := strings.Join(append(path, n.name), " ")
			if deprecatedPaths[fullPath] {
				t.Errorf("commandTree contains deprecated command path: %q", fullPath)
			}

			if len(n.children) == 0 {
				// Terminal leaf: must have a valid non-zero command ID and declared session class
				if n.id == cmdNone {
					t.Errorf("leaf %q has cmdNone", fullPath)
				}
				if n.session == sessionUnset {
					t.Errorf("leaf %q has sessionUnset", fullPath)
				}
				leafCount++
				foundCommands[fullPath] = n.id
			} else if fullPath == "install" {
				// Dual node: "install" is both an executable command and a namespace for "install options"
				if n.id != cmdInstall {
					t.Errorf("dual node %q has unexpected id %d", fullPath, n.id)
				}
				if n.session == sessionUnset {
					t.Errorf("dual node %q has sessionUnset", fullPath)
				}
				dualCount++
				foundCommands[fullPath] = n.id
			} else if n.id != cmdNone {
				// Pure namespace: must NOT have a command ID
				t.Errorf("internal namespace node %q has non-zero id %d", fullPath, n.id)
			}

			if len(n.children) != 0 {
				walk(append(path, n.name), n.children)
			}
		}
	}
	walk(nil, commandTree)

	if leafCount != 16 {
		t.Errorf("expected 16 terminal leaf commands, got %d", leafCount)
	}
	if dualCount != 1 {
		t.Errorf("expected 1 dual command ('install'), got %d", dualCount)
	}
	totalRunnable := leafCount + dualCount
	if totalRunnable != 17 {
		t.Fatalf("expected 17 total runnable commands, got %d", totalRunnable)
	}

	// Verify exact bidirectional match with expected command topology
	if len(foundCommands) != len(expectedCommands) {
		t.Fatalf("found %d commands, want %d", len(foundCommands), len(expectedCommands))
	}
	for wantPath, wantID := range expectedCommands {
		gotID, ok := foundCommands[wantPath]
		if !ok {
			t.Errorf("missing expected command %q in commandTree", wantPath)
		} else if gotID != wantID {
			t.Errorf("command %q has id %d, want %d", wantPath, gotID, wantID)
		}
	}
}

// TestEndToEndCleanBreakMigration verifies runtime migration behavior:
// Deprecated command invocations are rejected at parse time with exit code 2 (usage error)
// and emit specific, actionable migration hints.
func TestEndToEndCleanBreakMigration(t *testing.T) {
	isolateRoots(t)

	tests := []struct {
		name       string
		args       []string
		wantSubstr string
	}{
		{
			name:       "show builds",
			args:       []string{"show", "builds", "heroes_3"},
			wantSubstr: `command "show builds" has been replaced; use "goggo galaxy builds <game>"`,
		},
		{
			name:       "show manifest",
			args:       []string{"show", "manifest", "heroes_3"},
			wantSubstr: `command "show manifest" has been replaced; use "goggo galaxy manifest <game> [build]"`,
		},
		{
			name:       "show cdns",
			args:       []string{"show", "cdns", "heroes_3"},
			wantSubstr: `command "show cdns" has been replaced; use "goggo galaxy cdns <game> [build]"`,
		},
		{
			name:       "show bare",
			args:       []string{"show"},
			wantSubstr: `command "show" has been replaced; use "goggo galaxy builds <game>", "goggo galaxy manifest <game> [build]" or "goggo galaxy cdns <game> [build]"`,
		},
		{
			name:       "list details",
			args:       []string{"list", "details", "heroes_3"},
			wantSubstr: `command "list details" has been moved; use "goggo backup list [<game>...]"`,
		},
		{
			name:       "list json",
			args:       []string{"list", "json", "heroes_3"},
			wantSubstr: `command "list json" has been replaced; use "--json" with commands that support JSON output`,
		},
		{
			name:       "download",
			args:       []string{"download", "heroes_3"},
			wantSubstr: `command "download" has been moved; use "goggo backup download <game> [<file>...]"`,
		},
		{
			name:       "download file",
			args:       []string{"download", "file", "heroes_3", "12345"},
			wantSubstr: `command "download file" has been moved; use "goggo backup download <game> [<file>...]"`,
		},
		{
			name:       "auth logout",
			args:       []string{"auth", "logout"},
			wantSubstr: `command "auth logout" has been renamed to "auth clear" to reflect that only local credentials are removed; use "goggo auth clear"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := Run(tt.args, strings.NewReader(""), &stdout, &stderr)
			if code != 2 {
				t.Fatalf("args %v: exit code = %d, want 2 (usage error). stderr: %s", tt.args, code, stderr.String())
			}
			if !strings.Contains(stderr.String(), tt.wantSubstr) {
				t.Errorf("args %v: stderr does not contain %q. Got:\n%s", tt.args, tt.wantSubstr, stderr.String())
			}
		})
	}
}

// TestEndToEndBackupMutualExclusion verifies backup command mutual exclusion rules:
// 1. backup list accepts --json, rejects --type.
// 2. backup download <game> <file> enters single-file mode with --directory.
// 3. backup download <game> --type installer enters category mode with --directory.
// 4. backup download <game> <file> --type installer is rejected with exit code 2 (usage error).
func TestEndToEndBackupMutualExclusion(t *testing.T) {
	isolateRoots(t)
	cfg, err := newConfig()
	if err != nil {
		t.Fatalf("newConfig: %v", err)
	}

	// 1. backup list: accepts --json, rejects --type
	inv, err := parseArgs([]string{"backup", "list", "heroes_3", "--json"}, cfg)
	if err != nil {
		t.Fatalf("backup list --json should parse: %v", err)
	}
	if !inv.json {
		t.Errorf("backup list --json: expected json=true")
	}

	_, err = parseArgs([]string{"backup", "list", "heroes_3", "--type", "installer"}, cfg)
	if err == nil {
		t.Fatal("backup list --type should fail parsing")
	}
	if !isUsageError(err) {
		t.Errorf("backup list --type should return usage error, got: %v", err)
	}

	// 2. backup download single-file with --directory
	inv, err = parseArgs([]string{"backup", "download", "heroes_3", "45223", "--directory", "D:\\Sandbox"}, cfg)
	if err != nil {
		t.Fatalf("backup download single file should parse: %v", err)
	}
	if len(inv.args) != 2 || inv.args[1] != "45223" {
		t.Errorf("expected 2 args with file spec, got: %v", inv.args)
	}
	if inv.cfg.Directories.Directory != "D:\\Sandbox" {
		t.Errorf("expected directory D:\\Sandbox, got: %s", inv.cfg.Directories.Directory)
	}

	// 3. backup download --type installer with --directory
	inv, err = parseArgs([]string{"backup", "download", "heroes_3", "--type", "installer", "--directory", "D:\\Sandbox"}, cfg)
	if err != nil {
		t.Fatalf("backup download --type should parse: %v", err)
	}
	if !inv.typeSet {
		t.Errorf("expected typeSet=true")
	}
	if inv.cfg.Directories.Directory != "D:\\Sandbox" {
		t.Errorf("expected directory D:\\Sandbox, got: %s", inv.cfg.Directories.Directory)
	}

	// 4. Mutual exclusion: <file> + --type
	var stdout, stderr bytes.Buffer
	code := Run([]string{"backup", "download", "heroes_3", "45223", "--type", "installer"}, strings.NewReader(""), &stdout, &stderr)
	if code != 2 {
		t.Fatalf("backup download file + --type exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "cannot combine file selectors with --type") {
		t.Errorf("expected mutual exclusion error message, got: %s", stderr.String())
	}
}

// TestEndToEndZeroMatchGuard verifies zero-match guard execution:
// install <game> with an incompatible language (zh-Hans on a product with only en/fr)
// is intercepted at the plan stage, exits with code 1, and no transfer work is observable
// or produces artifacts after the zero-match guard.
func TestEndToEndZeroMatchGuard(t *testing.T) {
	deps := newInstallOptionsFixture(t)
	installDir := t.TempDir()

	var stdout, stderr bytes.Buffer
	code := runWithDeps([]string{
		"install",
		"--platform", "windows",
		"--language", "zh-Hans",
		"--arch", "x64",
		"--directory", installDir,
		"1207658991",
	}, strings.NewReader(""), &stdout, &stderr, deps)

	if code != 1 {
		t.Fatalf("zero-match install exit = %d, want 1. stdout: %s, stderr: %s", code, stdout.String(), stderr.String())
	}

	// Verify error message indicates no compatible content
	if !strings.Contains(stderr.String(), "no compatible content found") {
		t.Errorf("expected 'no compatible content found' in stderr, got: %s", stderr.String())
	}

	// Verify Transfer is NEVER entered:
	// 1. stdout must not contain transfer messages
	if strings.Contains(stdout.String(), "Installing →") || strings.Contains(stdout.String(), "Files completed") {
		t.Errorf("transfer activity detected in stdout: %s", stdout.String())
	}

	// 2. install directory must remain strictly empty
	entries, err := os.ReadDir(installDir)
	if err != nil {
		t.Fatalf("ReadDir installDir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("install directory must remain empty, found %d entries", len(entries))
	}
}

// TestEndToEndNativePathBoundaries verifies native path boundary enforcement:
// Real production planning and file calculation pipelines enforce:
//  1. Galaxy Install planning outputs (PlanResult.InstallPath, Expected.Destination)
//     use native backslashes on Windows and contain zero forward slashes.
//  2. Backup Download target paths (GameDetails.MakeFilepaths / GetFilepath) use native backslashes on Windows.
//  3. Manifest logical paths (item.Path) preserve forward slashes.
//  4. CompactPath mixed separator pre-normalization eliminates forward slashes.
func TestEndToEndNativePathBoundaries(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows native path boundaries verification is specific to Windows")
	}

	deps := newInstallOptionsFixture(t)
	cfg, err := newConfig()
	if err != nil {
		t.Fatalf("newConfig: %v", err)
	}
	cfg.Directories.Directory = `C:\Games`
	cfg.DownloadConfig.GalaxyPlatform = config.PlatformWindows
	cfg.DownloadConfig.GalaxyLanguage = config.LangEN
	cfg.DownloadConfig.GalaxyArch = config.ArchX64

	// 1. Galaxy Install planning execution through production BuildPlan
	d, err := core.OpenWith(context.Background(), cfg, nil, core.SessionRequest{Required: true}, deps)
	if err != nil {
		t.Fatalf("core.OpenWith: %v", err)
	}
	defer func() { _ = d.Close() }()

	req := core.NewInstallRequest(cfg, "1207658991", "", core.ProductRefExact)
	planRes, err := d.BuildPlan(context.Background(), req)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}

	// Verify InstallPath
	if strings.Contains(planRes.InstallPath, "/") {
		t.Errorf("InstallPath %q contains forward slash on Windows", planRes.InstallPath)
	}
	if !strings.Contains(planRes.InstallPath, `\`) {
		t.Errorf("InstallPath %q missing backslash on Windows", planRes.InstallPath)
	}

	// Verify Expected file destinations and logical path separation
	if len(planRes.Expected) == 0 {
		t.Fatal("BuildPlan produced 0 expected files")
	}
	for _, ef := range planRes.Expected {
		if strings.Contains(ef.Destination, "/") {
			t.Errorf("Expected destination %q contains forward slash on Windows", ef.Destination)
		}
		if !strings.Contains(ef.Destination, `\`) {
			t.Errorf("Expected destination %q missing backslash on Windows", ef.Destination)
		}

		// 3. Manifest logical path retains forward slash (never backslash)
		if strings.Contains(ef.Item.Path, `\`) {
			t.Errorf("Manifest logical path %q should retain forward slashes, found backslash", ef.Item.Path)
		}
	}

	// 2. Backup Download target paths
	gf := gamedetails.GameFile{
		ID:       "12345",
		Gamename: "worms_united",
		Path:     "setup.exe",
		Platform: config.PlatformWindows,
		Type:     config.GFBaseInstaller,
	}
	conf := config.DirectoryConfig{
		Directory:        `C:\Backups`,
		SubDirectories:   true,
		GameSubdir:       "%gamename%",
		InstallersSubdir: "%platform%",
	}
	gd := gamedetails.GameDetails{Installers: []gamedetails.GameFile{gf}}
	gd.MakeFilepaths(conf)

	targetPath := gd.Installers[0].GetFilepath()
	if strings.Contains(targetPath, "/") {
		t.Errorf("Backup download target path %q contains forward slash on Windows", targetPath)
	}
	if !strings.Contains(targetPath, `\`) {
		t.Errorf("Backup download target path %q missing backslash on Windows", targetPath)
	}

	// 4. CompactPath mixed separator pre-normalization
	compacted := compactPath(`C:\Games/Heroes 3/Data/Maps/map.h3m`, 25)
	if strings.Contains(compacted, "/") {
		t.Errorf("compactPath %q contains forward slash on Windows", compacted)
	}
}

// TestEndToEndSandboxPathIsolation verifies sandbox path isolation:
// Running a real install command through runWithDeps with --directory <sandbox>
// actually writes installation files to <sandbox>, while leaving the actual configured
// default working directory (defaultRoot, mapped via t.Chdir) completely unmodified
// (full bidirectional snapshot invariant).
func TestEndToEndSandboxPathIsolation(t *testing.T) {
	deps := newInstallOptionsFixture(t)

	// Create default working root, isolate cwd to it, and place sentinel files.
	// Since goggo defaults Directories.Directory to ".", t.Chdir makes defaultRoot
	// the actual production default directory.
	defaultRoot := t.TempDir()
	t.Chdir(defaultRoot)

	sentinel := filepath.Join(defaultRoot, "sentinel.dat")
	if err := os.WriteFile(sentinel, []byte("preserve-me"), 0o644); err != nil {
		t.Fatalf("WriteFile sentinel: %v", err)
	}

	beforeSnapshot := snapshotDirectory(t, defaultRoot)

	// Target sandbox
	sandbox := t.TempDir()

	// Execute REAL install command into sandbox
	var stdout, stderr bytes.Buffer
	code := runWithDeps([]string{
		"install",
		"--platform", "windows",
		"--language", "en",
		"--arch", "x64",
		"--directory", sandbox,
		"1207658991",
	}, strings.NewReader(""), &stdout, &stderr, deps)

	if code != 0 {
		t.Fatalf("install into sandbox exit = %d, want 0. stderr: %s", code, stderr.String())
	}

	// Verify sandbox ACTUALLY received files
	sandboxEntries, err := os.ReadDir(sandbox)
	if err != nil {
		t.Fatalf("ReadDir sandbox: %v", err)
	}
	if len(sandboxEntries) == 0 {
		t.Fatal("sandbox directory is empty; install failed to produce files")
	}

	// Verify defaultRoot is 100% unchanged (full bidirectional invariant)
	afterSnapshot := snapshotDirectory(t, defaultRoot)
	assertSnapshotsEqual(t, "defaultRoot", beforeSnapshot, afterSnapshot)
}

// TestEndToEndAuthClearSandbox verifies auth clear sandbox isolation:
// auth clear executed in an isolated APPDATA/LOCALAPPDATA sandbox deletes sandbox credentials,
// while the host credential tree remains strictly unchanged (full bidirectional set invariant).
func TestEndToEndAuthClearSandbox(t *testing.T) {
	hostAppDir := os.Getenv("AppData")
	var beforeHostSnap map[string]fileSnapshot
	hostGoggo := ""
	if hostAppDir != "" {
		hostGoggo = filepath.Join(hostAppDir, "goggo")
		beforeHostSnap = snapshotDirectory(t, hostGoggo)
	}

	// Set up sandbox environment
	sandboxDir := t.TempDir()
	t.Setenv("AppData", sandboxDir)
	t.Setenv("LOCALAPPDATA", sandboxDir)
	t.Setenv("XDG_CONFIG_HOME", sandboxDir)
	t.Setenv("XDG_CACHE_HOME", sandboxDir)
	t.Setenv("HOME", sandboxDir)

	cfg, err := newConfig()
	if err != nil {
		t.Fatalf("newConfig: %v", err)
	}
	if err := os.MkdirAll(cfg.ConfigDirectory, 0o755); err != nil {
		t.Fatal(err)
	}

	// Populate sandbox credentials
	dummyStore := filepath.Join(cfg.ConfigDirectory, "credentials.bin")
	if err := os.WriteFile(dummyStore, []byte("dummy-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	dummyCookies := filepath.Join(cfg.ConfigDirectory, "cookies.bin")
	if err := os.WriteFile(dummyCookies, []byte("dummy-cookies"), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{"auth", "clear"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("auth clear in sandbox exit = %d, want 0. stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Local login state cleared") {
		t.Errorf("stdout missing expected notice, got: %s", stdout.String())
	}

	// Verify sandbox credentials were removed
	if _, err := os.Stat(dummyStore); !os.IsNotExist(err) {
		t.Errorf("sandbox credentials.bin still exists: %v", err)
	}
	if _, err := os.Stat(dummyCookies); !os.IsNotExist(err) {
		t.Errorf("sandbox cookies.bin still exists: %v", err)
	}

	// Verify host credentials remain strictly unchanged (full bidirectional set invariant)
	if hostGoggo != "" {
		afterHostSnap := snapshotDirectory(t, hostGoggo)
		assertSnapshotsEqual(t, "hostGoggo", beforeHostSnap, afterHostSnap)
	}
}

type fileSnapshot struct {
	size  int64
	mtime time.Time
}

func snapshotDirectory(t *testing.T, root string) map[string]fileSnapshot {
	t.Helper()
	snap := make(map[string]fileSnapshot)
	if root == "" {
		return snap
	}
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return snap
	}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		snap[rel] = fileSnapshot{size: info.Size(), mtime: info.ModTime()}
		return nil
	})
	if err != nil {
		t.Fatalf("snapshotDirectory %q: %v", root, err)
	}
	return snap
}

func assertSnapshotsEqual(t *testing.T, label string, before, after map[string]fileSnapshot) {
	t.Helper()
	if len(before) != len(after) {
		t.Fatalf("%s: file count changed: before=%d, after=%d", label, len(before), len(after))
	}
	for rel, b := range before {
		a, ok := after[rel]
		if !ok {
			t.Errorf("%s: file deleted: %s", label, rel)
		} else if a.size != b.size {
			t.Errorf("%s: file size changed for %s: before=%d, after=%d", label, rel, b.size, a.size)
		} else if !a.mtime.Equal(b.mtime) {
			t.Errorf("%s: file mtime changed for %s: before=%v, after=%v", label, rel, b.mtime, a.mtime)
		}
	}
	for rel := range after {
		if _, ok := before[rel]; !ok {
			t.Errorf("%s: unexpected new file created: %s", label, rel)
		}
	}
}
