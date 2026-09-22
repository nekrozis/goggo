package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/nekrozis/goggo/internal/model"
)

// orphansFixture lays out an install root: three files the installation owns
// (they are the plan's expected set), and one true orphan.
//
// The ledger is PlanResult.Expected, the same set a verification reads, so the
// fixture states the installed paths directly instead of assembling them from
// tasks, containers and skipped destinations.
type orphansFixture struct {
	root string
	res  PlanResult
}

func newOrphansFixture(t *testing.T) *orphansFixture {
	t.Helper()
	f := &orphansFixture{root: t.TempDir()}
	f.res = PlanResult{
		InstallPath: f.root,
		Expected: []InstalledFile{
			{Destination: f.root + "/game/data.bin", Item: model.GalaxyDepotItem{Path: "game/data.bin"}},
			{Destination: f.root + "/game/readme.txt", Item: model.GalaxyDepotItem{Path: "game/readme.txt"}},
			// A small-files member is an expected file like any other: the walk
			// must not report it even though no container mentions it.
			{Destination: f.root + "/game/small1.txt",
				Item: model.GalaxyDepotItem{Path: "game/small1.txt", SFCOffset: 0, SFCSize: 4}},
		},
	}
	for _, p := range []string{"game/data.bin", "game/readme.txt", "game/small1.txt", "leftover.bin"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(f.root, p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(f.root, p), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

// TestCheckOrphanedFiles counts without deleting: the leftover file surfaces
// as the one orphan, the SFC member counts as installed, and nothing is
// removed while --delete-orphans is off.
func TestCheckOrphanedFiles(t *testing.T) {
	f := newOrphansFixture(t)
	cfg := planTestConfig(t)
	cfg.DownloadConfig.DeleteOrphans = false
	d := newOfflineDownloader(t, noopServer(t), cfg, newFakeConsole())

	if err := d.CheckOrphanedFiles(context.Background(), f.res); err != nil {
		t.Fatalf("CheckOrphanedFiles: %v", err)
	}
	// The count line is the walk's observable: its tab-separated count token
	// keeps "11 orphaned files" from satisfying "1 orphaned files".
	out := consoleText(t, d)
	if !strings.Contains(out, "\t1 orphaned files") {
		t.Errorf("output = %q, want the orphan count", out)
	}
	if _, err := os.Stat(filepath.Join(f.root, "leftover.bin")); err != nil {
		t.Error("leftover.bin was removed while --delete-orphans is off")
	}
}

// TestCheckOrphanedFilesDelete locks the deletion gate: with the option set,
// the orphan goes and the installed files stay.
func TestCheckOrphanedFilesDelete(t *testing.T) {
	f := newOrphansFixture(t)
	cfg := planTestConfig(t)
	cfg.DownloadConfig.DeleteOrphans = true
	d := newOfflineDownloader(t, noopServer(t), cfg, newFakeConsole())

	if err := d.CheckOrphanedFiles(context.Background(), f.res); err != nil {
		t.Fatalf("CheckOrphanedFiles: %v", err)
	}
	assertFileAbsent(t, filepath.Join(f.root, "leftover.bin"))
	assertFileContent(t, filepath.Join(f.root, "game", "data.bin"), "x")
	assertFileContent(t, filepath.Join(f.root, "game", "small1.txt"), "x")
}

// TestCheckOrphanedFilesIgnorelist locks the D-G4 filter: a file on the
// ignorelist is skipped during the walk — it is neither counted as an orphan
// nor deleted, and the verbose notice lands on the error stream.
func TestCheckOrphanedFilesIgnorelist(t *testing.T) {
	f := newOrphansFixture(t)
	cfg := planTestConfig(t)
	cfg.DownloadConfig.DeleteOrphans = true
	cfg.MsgLevel = 2 // verbose: the skip notices must be rendered
	ignorePath := filepath.Join(t.TempDir(), "ignorelist.txt")
	if err := os.WriteFile(ignorePath, []byte("R leftover"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg.IgnorelistFilePath = ignorePath
	d := newOfflineDownloader(t, noopServer(t), cfg, newFakeConsole())

	if err := d.CheckOrphanedFiles(context.Background(), f.res); err != nil {
		t.Fatalf("CheckOrphanedFiles: %v", err)
	}
	// The ignorelisted file survives deletion and is not an orphan.
	assertFileContent(t, filepath.Join(f.root, "leftover.bin"), "x")
	out := consoleText(t, d)
	if strings.Contains(out, "\t1 orphaned files") {
		t.Errorf("the ignorelisted file was counted: %s", out)
	}
	if !strings.Contains(out, "\t0 orphaned files") {
		t.Errorf("output = %q, want 0 orphans after the ignorelist skip", out)
	}
	if errText := consoleErrText(t, d); !strings.Contains(errText, "skipped ignorelisted file") {
		t.Errorf("stderr = %q, want the verbose skip notice", errText)
	}
}

// consoleErrText pulls the error-stream output the fake console captured.
func consoleErrText(t *testing.T, d *Downloader) string {
	t.Helper()
	c, ok := d.ui.(*fakeConsole)
	if !ok {
		t.Fatalf("ui is %T, want *fakeConsole", d.ui)
	}
	return c.errOut.String()
}

// TestCheckOrphanedFilesIgnorelistReadError locks the D-G3 boundary: a
// read failure on the ignorelist file is an error, never a silent empty filter.
func TestCheckOrphanedFilesIgnorelistReadError(t *testing.T) {
	f := newOrphansFixture(t)
	cfg := planTestConfig(t)
	// A directory in place of the file makes the read fail without absence.
	cfg.IgnorelistFilePath = t.TempDir()
	d := newOfflineDownloader(t, noopServer(t), cfg, newFakeConsole())

	if err := d.CheckOrphanedFiles(context.Background(), f.res); err == nil {
		t.Fatal("CheckOrphanedFiles = nil, want the ignorelist read error")
	}
}

// TestCheckOrphanedFilesExpectedLedger locks where the ledger comes from: the
// walk keys on the plan's expected set and on nothing else. A file
// the installation owns but which appears in no task, no container and no
// skipped list is neither reported nor deleted — the ledger this step replaced
// would have rebuilt the set from exactly those three sources and deleted it.
func TestCheckOrphanedFilesExpectedLedger(t *testing.T) {
	f := newOrphansFixture(t)
	owned := filepath.Join(f.root, "game", "owned.bin")
	if err := os.WriteFile(owned, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.res.Expected = append(f.res.Expected, InstalledFile{
		Destination: owned,
		Item:        model.GalaxyDepotItem{Path: "game/owned.bin"},
	})

	cfg := planTestConfig(t)
	cfg.DownloadConfig.DeleteOrphans = true
	d := newOfflineDownloader(t, noopServer(t), cfg, newFakeConsole())

	if err := d.CheckOrphanedFiles(context.Background(), f.res); err != nil {
		t.Fatalf("CheckOrphanedFiles: %v", err)
	}
	assertFileContent(t, owned, "x")
	assertFileAbsent(t, filepath.Join(f.root, "leftover.bin"))
	if out := consoleText(t, d); !strings.Contains(out, "\t1 orphaned files") {
		t.Errorf("output = %q, want only the true leftover counted", out)
	}
}

// TestCheckOrphansReportsTheUnaccountedFiles walks a real installation built from
// a real plan: the files the manifest expects are silent, and everything else is
// an orphan — a leftover, the install metadata file (no special case), a
// small-files container that outlived its extraction (the one deliberate
// change of this step) — while a directory is never one.
func TestCheckOrphansReportsTheUnaccountedFiles(t *testing.T) {
	f := newVerifyFixture(t)
	container := "galaxy_smallfilescontainer_" + planProductID
	info := "goggame-" + planProductID + ".info"

	f.place(t, "game/data.bin", verifyPlain)
	f.place(t, "leftover.bin", []byte("x"))
	f.place(t, info, []byte("{}"))
	f.place(t, container, []byte("x"))
	if err := os.MkdirAll(filepath.Join(f.root, "emptydir"), 0o755); err != nil {
		t.Fatal(err)
	}

	res, err := f.downloader(t).CheckOrphans(context.Background(), NewInstallRequest(f.cfg, planProductID, "", ProductRefExact))
	if err != nil {
		t.Fatalf("CheckOrphans: %v", err)
	}
	if res.InstallPath != f.root {
		t.Errorf("InstallPath = %q, want %q", res.InstallPath, f.root)
	}

	var got []string
	for _, path := range res.Files {
		got = append(got, res.Relative(path))
	}
	sort.Strings(got)
	want := []string{container, info, "leftover.bin"}
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("orphans = %v, want %v", got, want)
	}
	// No absolute path leaks into the listing: the header names the root.
	for _, path := range res.Files {
		if strings.HasPrefix(res.Relative(path), f.root) {
			t.Errorf("%s was not made relative to the root", path)
		}
	}
}

// TestCheckOrphansCarriesTheWalkDiagnostics locks the filter files' notices: they
// come back as data with the error flag set, so the front end decides the stream
// — core prints nothing on this path.
func TestCheckOrphansCarriesTheWalkDiagnostics(t *testing.T) {
	f := newOrphansFixture(t)
	cfg := planTestConfig(t)
	cfg.MsgLevel = msgLevelVerbose
	ignorePath := filepath.Join(t.TempDir(), "ignorelist.txt")
	if err := os.WriteFile(ignorePath, []byte("R leftover"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg.IgnorelistFilePath = ignorePath
	d := newOfflineDownloader(t, noopServer(t), cfg, newFakeConsole())

	files, notices, err := d.walkOrphans(f.root, f.res.Expected)
	if err != nil {
		t.Fatalf("walkOrphans: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("orphans = %v, want none: the only leftover is ignorelisted", files)
	}
	if len(notices) != 1 || !notices[0].Err || !strings.Contains(notices[0].Text, "skipped ignorelisted file") {
		t.Errorf("notices = %+v, want the verbose skip as an error-stream notice", notices)
	}
	if out := consoleText(t, d); out != "" {
		t.Errorf("stdout = %q, want nothing: the walk does not print", out)
	}
}

// TestCheckOrphansIsReadOnly locks the read-only promise of `orphans check`
// : the walk changes neither the content nor the
// modification time of anything it finds, orphans included.
func TestCheckOrphansIsReadOnly(t *testing.T) {
	f := newVerifyFixture(t)
	f.place(t, "game/data.bin", verifyPlain)
	f.place(t, "leftover.bin", []byte("x"))

	before := treeState(t, f.root)
	res, err := f.downloader(t).CheckOrphans(context.Background(), NewInstallRequest(f.cfg, planProductID, "", ProductRefExact))
	if err != nil {
		t.Fatalf("CheckOrphans: %v", err)
	}
	if len(res.Files) != 1 {
		t.Fatalf("orphans = %v, want the one leftover", res.Files)
	}

	after := treeState(t, f.root)
	if len(after) != len(before) {
		t.Fatalf("the tree has %d files after the walk, had %d", len(after), len(before))
	}
	for path, want := range before {
		if got, ok := after[path]; !ok || got != want {
			t.Errorf("%s changed: %+v, want %+v", path, got, want)
		}
	}
}

// TestRemoveOrphansDeletesExactlyTheList locks the removal contract:
// core deletes the paths it was handed, in order, and nothing else — a file that
// exists but is not on the list survives, and one unremovable path does not stop
// the batch.
func TestRemoveOrphansDeletesExactlyTheList(t *testing.T) {
	dir := t.TempDir()
	listed := filepath.Join(dir, "listed.bin")
	second := filepath.Join(dir, "second.bin")
	unlisted := filepath.Join(dir, "unlisted.bin")
	for _, path := range []string{listed, second, unlisted} {
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A NUL byte makes one path impossible to remove on every platform, so the
	// failure is the code's and not the environment's.
	unremovable := "x\x00y"

	d := newOfflineDownloader(t, noopServer(t), planTestConfig(t), newFakeConsole())
	res := OrphansResult{InstallPath: dir, Files: []string{listed, unremovable, second}}

	attempts, err := d.RemoveOrphans(context.Background(), res)
	if err != nil {
		t.Fatalf("RemoveOrphans: %v", err)
	}
	if len(attempts) != 3 {
		t.Fatalf("attempts = %d, want one per listed file", len(attempts))
	}
	if attempts[0].Path != listed || attempts[0].Err != nil {
		t.Errorf("attempt[0] = %+v, want %s removed", attempts[0], listed)
	}
	if attempts[1].Path != unremovable || attempts[1].Err == nil {
		t.Errorf("attempt[1] = %+v, want the unremovable path reported", attempts[1])
	}
	if attempts[2].Path != second || attempts[2].Err != nil {
		t.Errorf("attempt[2] = %+v, want the batch to have continued", attempts[2])
	}
	assertFileAbsent(t, listed)
	assertFileAbsent(t, second)
	assertFileContent(t, unlisted, "x")

	// A cancelled run removes nothing: the caller interrupted before the first
	// deletion, and a destructive batch must not start on its way out.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	attempts, err = d.RemoveOrphans(ctx, OrphansResult{InstallPath: dir, Files: []string{unlisted}})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want the cancellation", err)
	}
	if len(attempts) != 0 {
		t.Errorf("attempts = %+v, want none after a cancelled run", attempts)
	}
	assertFileContent(t, unlisted, "x")
}

// TestCheckOrphanedFilesDeleteListsObjects locks that a destructive delete names
// its objects. The header aggregates the scale, each
// removed file gets one indented line relative to the install root, and a
// zero-orphan run prints no header at all (the count line above already said
// so). A failed delete stays its own stderr diagnostic.
func TestCheckOrphanedFilesDeleteListsObjects(t *testing.T) {
	f := newOrphansFixture(t)
	// Add a nested orphan to prove the relative display path.
	if err := os.MkdirAll(filepath.Join(f.root, "mods", "hd"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.root, "mods", "hd", "patch.dll"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := planTestConfig(t)
	cfg.DownloadConfig.DeleteOrphans = true
	d := newOfflineDownloader(t, noopServer(t), cfg, newFakeConsole())

	if err := d.CheckOrphanedFiles(context.Background(), f.res); err != nil {
		t.Fatalf("CheckOrphanedFiles: %v", err)
	}
	out := consoleText(t, d)
	if !strings.Contains(out, "Deleting 2 orphaned files") {
		t.Errorf("output = %q, want the scale header", out)
	}
	if !strings.Contains(out, "  "+filepath.FromSlash("mods/hd/patch.dll")) {
		t.Errorf("output = %q, want the relative per-object line", out)
	}
	if strings.Contains(out, filepath.Join(f.root, "mods")) {
		t.Errorf("output = %q, want no absolute per-object lines", out)
	}
	assertFileAbsent(t, filepath.Join(f.root, "mods", "hd", "patch.dll"))
	assertFileAbsent(t, filepath.Join(f.root, "leftover.bin"))
}
