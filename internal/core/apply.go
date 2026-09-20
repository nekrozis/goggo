package core

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/nekrozis/goggo/internal/model"
)

// ApplyPlanChanges executes the plan's destructive pre-work: the old-build
// deletions, then the parent directories of every task. Per-item failures are
// reported as error notices and skipped, non-fatally; a whole-function error
// means the run cannot continue — a cancelled context returns ctx.Err() with
// the notices collected so far.
//
// It does not create the task files themselves, download anything or clean up
// partial downloads: those belong to the transfer and its failure handling.
func (d *Downloader) ApplyPlanChanges(ctx context.Context, plan model.DownloadPlan) ([]Notice, error) {
	var notices []Notice
	for _, path := range plan.Deletes {
		if err := ctx.Err(); err != nil {
			return notices, err
		}
		// A path that is already gone is fine; anything else that fails is
		// reported and skipped.
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			notices = append(notices, Notice{Text: "Failed to delete " + path, Err: true})
		}
	}
	for _, task := range plan.Tasks {
		if err := ctx.Err(); err != nil {
			return notices, err
		}
		dir := filepath.Dir(task.Destination)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			notices = append(notices, Notice{Text: "Failed to create directory: " + dir, Err: true})
		}
	}
	return notices, nil
}
