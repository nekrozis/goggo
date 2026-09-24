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

// OrphansResult is what one orphan walk observed: the files under the install
// root that no expected path accounts for, plus the diagnostics the walk and the
// plan produced.
//
// Files is the whole contract of the walk: a caller lists it, shows it, and
// hands exactly this value back for deletion — nothing re-derives the set, so
// what was shown is what is removed.
type OrphansResult struct {
	// InstallPath is the root the walk covered and the base Relative uses.
	InstallPath string

	// Files are the orphaned paths in walk order, absolute.
	Files []string

	// Notices are the diagnostics: the plan's own lines (the verbose item
	// listing, the blacklist report) followed by the walk's (the verbose lines
	// for the paths the filter files excluded).
	Notices []Notice
}

// Relative is the install-root-relative form of a path the walk produced; a path
// outside the root keeps its absolute form (it cannot be made relative). It is
// the one implementation of that rule, shared by the install tail and the front
// end.
func (r OrphansResult) Relative(path string) string { return orphanDisplayPath(r.InstallPath, path) }

// CheckOrphans builds the read-only plan for one install request, walks the
// installation and reports the files no expected path accounts for.
//
// The walk is the one the install tail has always run; only the ledger's SOURCE
// changed: it is the plan's Expected set, the same one a verification reads,
// instead of a set rebuilt from the tasks, the containers and the skipped
// destinations. A consequence is deliberate: a small-files container that
// outlived its extraction is reported as an orphan, since it is not part of the
// finished installation.
func (d *Downloader) CheckOrphans(ctx context.Context, req InstallRequest) (OrphansResult, error) {
	res, err := d.buildPlan(ctx, req, planForReadOnly)
	out := OrphansResult{InstallPath: res.InstallPath, Notices: res.Messages}
	if err != nil {
		return out, err
	}
	files, notices, err := d.walkOrphans(res.InstallPath, res.Expected)
	out.Files = files
	out.Notices = append(out.Notices, notices...)
	return out, err
}

// DeletionAttempt is one deletion of one orphaned file. Err is nil when the file
// was removed; anything else is that file's own failure, reported while the rest
// of the batch goes on.
type DeletionAttempt struct {
	Err  error
	Path string
}

// RemoveOrphans deletes exactly the files an OrphansResult lists, in order, and
// reports the outcome of each attempt. It never re-derives the set: the contract
// is "the caller showed this list and the user authorized it".
//
// A cancelled context stops the loop; the attempts made so far come back with
// the error, because a destructive operation has to be auditable even when it
// was interrupted.
func (d *Downloader) RemoveOrphans(ctx context.Context, res OrphansResult) ([]DeletionAttempt, error) {
	return d.deleteOrphans(ctx, res.Files)
}

// CheckOrphanedFiles reports — and, when the legacy configuration gate is on,
// removes — the files under the install root that no expected path accounts for.
// It runs inline at the tail of an install.
//
// Under this CLI the gate is unreachable: --delete-orphans is gone and the
// destructive path is the `orphans remove` command. The field and the branch
// stay because they mirror the configuration, which the option table may carry
// again.
func (d *Downloader) CheckOrphanedFiles(ctx context.Context, res PlanResult) error {
	fmt.Fprintln(d.ui.Out(), "Checking for orphaned files")

	files, notices, err := d.walkOrphans(res.InstallPath, res.Expected)
	d.emitNotices(notices)
	if err != nil {
		return err
	}
	fmt.Fprintf(d.ui.Out(), "\t%d orphaned files\n", len(files))

	if !d.cfg.DownloadConfig.DeleteOrphans {
		return nil
	}
	// A deletion is a destructive action, so its objects stay auditable: a
	// header states the scale, then one indented line names each file actually
	// removed — a "deleted N" summary alone would hide WHICH mods or patches
	// disappeared. The header only appears when there is something to delete.
	if len(files) > 0 {
		fmt.Fprintf(d.ui.Out(), "Deleting %d orphaned files\n", len(files))
	}
	attempts, err := d.deleteOrphans(ctx, files)
	for _, attempt := range attempts {
		if attempt.Err != nil {
			fmt.Fprintln(d.ui.ErrOut(), "Failed to delete "+attempt.Path)
			continue
		}
		fmt.Fprintf(d.ui.Out(), "  %s\n", orphanDisplayPath(res.InstallPath, attempt.Path))
	}
	return err
}

// walkOrphans is the walk itself, and it takes exactly what it reads: the root
// and the set of paths the installation owns. Nothing here can reach back into
// the plan's tasks or containers, which is what keeps the ledger single.
//
// The filter files are consulted with the same absolute-path matching the plan
// builder uses, ignorelist first; an excluded path is neither reported nor
// deleted. It takes no context on purpose: an install's orphan walk has never
// been interruptible, and cancellation stops the deletion loop instead, which is
// where an interruption has something to preserve.
func (d *Downloader) walkOrphans(root string, expected []InstalledFile) ([]string, []Notice, error) {
	installed := make(map[string]bool, len(expected))
	for _, file := range expected {
		installed[file.Item.Path] = true
	}

	il, err := blacklist.LoadBlacklist(d.cfg.IgnorelistFilePath)
	if err != nil {
		return nil, nil, err
	}
	bl, err := blacklist.LoadBlacklist(d.cfg.BlacklistFilePath)
	if err != nil {
		return nil, nil, err
	}

	var (
		orphans []string
		notices []Notice
	)
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if il.IsBlacklisted(path) {
			if d.cfg.MsgLevel >= msgLevelVerbose {
				notices = append(notices, Notice{Text: "skipped ignorelisted file " + path, Err: true})
			}
			return nil
		}
		if bl.IsBlacklisted(path) {
			if d.cfg.MsgLevel >= msgLevelVerbose {
				notices = append(notices, Notice{Text: "skipped blacklisted file " + path, Err: true})
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
	if err != nil {
		return orphans, notices, err
	}
	return orphans, notices, nil
}

// deleteOrphans removes the files one by one, in the order given, collecting an
// attempt per file instead of stopping at the first failure: a batch delete that
// abandons the rest because one path was refused leaves the installation in a
// state nobody chose.
func (d *Downloader) deleteOrphans(ctx context.Context, files []string) ([]DeletionAttempt, error) {
	attempts := make([]DeletionAttempt, 0, len(files))
	for _, path := range files {
		if ctx.Err() != nil {
			return attempts, ctx.Err()
		}
		attempts = append(attempts, DeletionAttempt{Path: path, Err: os.Remove(path)})
	}
	return attempts, nil
}

// orphanDisplayPath is the install-root-relative form of a walk path; a path
// outside the root keeps its absolute form (it cannot be made relative).
func orphanDisplayPath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return filepath.Clean(rel)
}
