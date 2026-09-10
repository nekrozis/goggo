package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nekrozis/goggo/internal/model"
)

// orphansFixture lays out an install root: two installed files (they are the
// plan's items), a small-files member, and one true orphan.
type orphansFixture struct {
	root string
	res  PlanResult
	cfg  func() // mutates the downloader's config before the check runs
}

func newOrphansFixture(t *testing.T) *orphansFixture {
	t.Helper()
	f := &orphansFixture{root: t.TempDir()}
	f.res = PlanResult{
		InstallPath: f.root,
		Plan: model.DownloadPlan{
			Tasks: []model.FileTask{
				{Item: model.GalaxyDepotItem{Path: "game/data.bin"}, Destination: f.root + "/game/data.bin"},
				{Item: model.GalaxyDepotItem{Path: "game/readme.txt"}, Destination: f.root + "/game/readme.txt"},
			},
			SFC: []model.SFCGroup{{
				Container: model.GalaxyDepotItem{Path: "container.bin", ProductID: "42"},
				Items:     []model.GalaxyDepotItem{{Path: "game/small1.txt", ProductID: "42", SFCOffset: 0, SFCSize: 4}},
			}},
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
	out := consoleText(t, d)
	if !strings.Contains(out, "Checking for orphaned files") || !strings.Contains(out, "\t1 orphaned files") {
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
