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
// the way bDeleteOrphans gates it upstream. The walk consults both filter
// files, ignorelist first then blacklist, the way the C++ source does
// (downloader.cpp:4326-4336).
func (d *Downloader) CheckOrphanedFiles(ctx context.Context, res PlanResult) error {
	fmt.Fprintln(d.ui.Out(), "Checking for orphaned files")

	installed := map[string]bool{}
	for _, task := range res.Plan.Tasks {
		installed[task.Item.Path] = true
	}
	for _, path := range planSFCMemberPaths(res.Plan) {
		installed[path] = true
	}
	// Destinations the plan observed as already up to date are part of the
	// target installation even though they produce no download task — they
	// must not be reported as orphans (or deleted), because the installed set
	// describes which paths are valid for the target installation, not which
	// paths produced a download task (review RES1, decisions D44). The set is
	// keyed by the item-relative path, the same form the walk compares.
	for _, sf := range res.Skipped {
		installed[sf.Item.Path] = true
	}

	il, err := blacklist.LoadBlacklist(d.cfg.IgnorelistFilePath)
	if err != nil {
		return err
	}
	bl, err := blacklist.LoadBlacklist(d.cfg.BlacklistFilePath)
	if err != nil {
		return err
	}

	orphans, err := d.orphanedFiles(res, il, bl, installed)
	if err != nil {
		return err
	}
	fmt.Fprintf(d.ui.Out(), "\t%d orphaned files\n", len(orphans))

	if !d.cfg.DownloadConfig.DeleteOrphans {
		return nil
	}
	// A deletion is a destructive action, so its objects stay auditable by
	// default (review UI1-R2 §5, amended): a header states the scale, then
	// one indented line names each file actually removed — a "deleted N"
	// summary alone would hide WHICH mods or patches disappeared. The lines
	// are relative to the install root; a failed delete stays its own
	// diagnostic. The header only appears when there is something to delete:
	// the count line above already said "0 orphaned files".
	if len(orphans) > 0 {
		fmt.Fprintf(d.ui.Out(), "Deleting %d orphaned files\n", len(orphans))
	}
	for _, path := range orphans {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := os.Remove(path); err != nil {
			fmt.Fprintln(d.ui.ErrOut(), "Failed to delete "+path)
			continue
		}
		fmt.Fprintf(d.ui.Out(), "  %s\n", orphanDisplayPath(res.InstallPath, path))
	}
	return nil
}

// orphanDisplayPath is the install-root-relative form of a walk path; a path
// outside the root keeps its absolute form (it cannot be made relative).
func orphanDisplayPath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(rel)
}

// orphanedFiles walks the install root and collects the files whose full path
// no depot item carries. Directories are never orphans, and both filter files
// are consulted with the same absolute-path matching the plan builder uses —
// ignorelist first, then blacklist, the way the C++ source orders them
// (downloader.cpp:4326-4336). There is no special case for the install
// metadata file: upstream has none either, so a goggame-*.info from a previous
// installation counts as an orphan there as it does here.
func (d *Downloader) orphanedFiles(res PlanResult, il, bl *blacklist.Blacklist, installed map[string]bool) ([]string, error) {
	var orphans []string
	root := res.InstallPath
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if il.IsBlacklisted(path) {
			if d.cfg.MsgLevel >= msgLevelVerbose {
				fmt.Fprintln(d.ui.ErrOut(), "skipped ignorelisted file "+path)
			}
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
