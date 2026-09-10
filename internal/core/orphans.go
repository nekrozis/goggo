package core

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/nekrozis/goggo/internal/blacklist"
)

// CheckOrphanedFiles reports — and, when deletion is configured, removes —
// files under the install root that no depot item accounts for
// (downloader.cpp:4312-4343). It runs inline at the tail of an install, after
// the small-files containers have been unpacked; the members count as installed
// files, which is why they are added back to the set (downloader.cpp:4303-4306).
//
// The count always prints; the deletion happens only under --delete-orphans,
// the way bDeleteOrphans gates it upstream. Files on the blacklist are skipped
// during the walk (downloader.cpp:4326-4336); the ignorelist pass has no Go
// counterpart yet, so ignorelisted files would surface as orphans — Δ
// recorded (review S20).
func (d *Downloader) CheckOrphanedFiles(ctx context.Context, res PlanResult) error {
	fmt.Fprintln(d.ui.Out(), "Checking for orphaned files")

	installed := map[string]bool{}
	for _, task := range res.Plan.Tasks {
		installed[task.Item.Path] = true
	}
	for _, path := range planSFCMemberPaths(res.Plan) {
		installed[path] = true
	}

	bl, err := blacklist.LoadBlacklist(d.cfg.BlacklistFilePath)
	if err != nil {
		return err
	}

	orphans, err := d.orphanedFiles(res, bl, installed)
	if err != nil {
		return err
	}
	fmt.Fprintf(d.ui.Out(), "\t%d orphaned files\n", len(orphans))

	if !d.cfg.DownloadConfig.DeleteOrphans {
		return nil
	}
	for _, path := range orphans {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		fmt.Fprintln(d.ui.Out(), "Deleting "+path)
		if err := os.Remove(path); err != nil {
			fmt.Fprintln(d.ui.ErrOut(), "Failed to delete "+path)
		}
	}
	return nil
}

// orphanedFiles walks the install root and collects the files whose full path
// no depot item carries. Directories are never orphans, and the blacklist is
// consulted with the same absolute-path matching the plan builder uses. There
// is no special case for the install metadata file: upstream has none either,
// so a goggame-*.info from a previous installation counts as an orphan there
// as it does here.
func (d *Downloader) orphanedFiles(res PlanResult, bl *blacklist.Blacklist, installed map[string]bool) ([]string, error) {
	var orphans []string
	root := res.InstallPath
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if bl.IsBlacklisted(path) {
			if d.cfg.MsgLevel >= msgLevelVerbose {
				fmt.Fprintln(d.ui.ErrOut(), "skipped blacklisted file "+path)
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = strings.ReplaceAll(rel, string(filepath.Separator), "/")
		if installed[rel] {
			return nil
		}
		orphans = append(orphans, path)
		return nil
	})
	return orphans, err
}
