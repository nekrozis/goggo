package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/nekrozis/goggo/internal/model"
)

// TestApplyPlanChangesDeletes locks the destructive half: a path that exists
// goes away, a path that is already gone is silent, and neither is an error.
func TestApplyPlanChangesDeletes(t *testing.T) {
	dir := t.TempDir()
	gone := filepath.Join(dir, "old.bin")
	if err := os.WriteFile(gone, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	d := &Downloader{}
	failures, err := d.ApplyPlanChanges(context.Background(), model.DownloadPlan{
		Deletes: []string{gone, filepath.Join(dir, "already-gone.bin")},
	})
	if err != nil {
		t.Fatalf("ApplyPlanChanges: %v", err)
	}
	if len(failures) != 0 {
		t.Errorf("failures = %+v, want none", failures)
	}
	if _, err := os.Stat(gone); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the delete target still exists")
	}
}

// TestApplyPlanChangesCreatesTaskParents locks the mkdir half: every task's
// parent directory exists afterwards, so the transfer can append directly.
func TestApplyPlanChangesCreatesTaskParents(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "nested", "deeper", "data.bin")
	d := &Downloader{}

	if _, err := d.ApplyPlanChanges(context.Background(), model.DownloadPlan{
		Tasks: []model.FileTask{{Destination: dest}},
	}); err != nil {
		t.Fatalf("ApplyPlanChanges: %v", err)
	}
	if fi, err := os.Stat(filepath.Dir(dest)); err != nil || !fi.IsDir() {
		t.Errorf("parent of %q was not created", dest)
	}
}

// TestApplyPlanChangesMkdirFailureIsNonFatal locks that a directory that cannot
// be created (a file sits at its path) is reported as a notice while the run
// continues.
func TestApplyPlanChangesMkdirFailureIsNonFatal(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	d := &Downloader{}
	failures, err := d.ApplyPlanChanges(context.Background(), model.DownloadPlan{
		Tasks: []model.FileTask{{Destination: filepath.Join(blocker, "sub", "data.bin")}},
	})
	if err != nil {
		t.Fatalf("err = %v, want a non-fatal report", err)
	}
	if len(failures) != 1 || !failures[0].Err {
		t.Errorf("failures = %+v, want one error notice", failures)
	}
}

// TestApplyPlanChangesContextCancel locks that a cancelled context comes back
// instead of being swallowed by the per-item continuation.
func TestApplyPlanChangesContextCancel(t *testing.T) {
	dir := t.TempDir()
	gone := filepath.Join(dir, "old.bin")
	if err := os.WriteFile(gone, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d := &Downloader{}
	if _, err := d.ApplyPlanChanges(ctx, model.DownloadPlan{Deletes: []string{gone}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
