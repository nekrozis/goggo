// Package reconcile observes local files against a GalaxyDepotItem and decides
// how an install must treat them: skip, download, replace or resume.
//
// It is a low-level observation package: it depends only on internal/model and
// the standard library, is used by both internal/core (plan time) and
// internal/transfer (authoritative re-check at task start), and NEVER modifies
// the filesystem — every entry point is observation-only. The decision is
// authoritative for the caller; the plan's own classification is an
// optimisation and a report, never the correctness boundary (review RES1 v2/v3,
// decisions D13-D19, D42-D44).
//
// Resume semantics (decisions D6/D15): a partial file is only resumable when
// its size lands exactly on an uncompressed chunk boundary AND the chunk before
// that boundary matches its uncompressed md5. Anything else is replaced.
// Correctness for the whole file is backstopped by the final Item.MD5 check on
// the next reconcile; no full-prefix rehash is performed.
package reconcile

import (
	"crypto/md5"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"

	"github.com/nekrozis/goggo/internal/model"
)

// Decision is the authoritative action an install must take for one
// destination, derived from the observed filesystem state.
type Decision uint8

const (
	// DecisionDownload writes from chunk 0. It does NOT imply the destination
	// is absent: a zero-length file also takes this branch, because appending
	// to an empty file is identical to downloading from scratch.
	DecisionDownload Decision = iota
	// DecisionSkip means the destination already satisfies the item: no
	// transfer needed.
	DecisionSkip
	// DecisionReplace means the existing content must be removed before
	// writing from chunk 0.
	DecisionReplace
	// DecisionResume means chunks [0, startChunk) are already on disk and
	// verified; the transfer starts at startChunk.
	DecisionResume
)

// IsComplete reports whether the destination already satisfies the item:
// the uncompressed size matches and the whole-file md5 matches.
//
// A zero-size item is its own special case — missing ⇒ false, size == 0 ⇒
// true, size > 0 ⇒ false — so a plan never marks an absent empty file as
// skipped (review RES1 v3 §4).
//
// The returned error signals an observation failure (unreadable file): it is
// an installation error, never "the content does not match".
func IsComplete(item model.GalaxyDepotItem, path string) (bool, error) {
	if item.TotalSize == 0 {
		fi, err := os.Stat(path)
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		return fi.Size() == 0, nil
	}
	fi, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if fi.Size() != int64(item.TotalSize) {
		return false, nil
	}
	got, err := fileMD5(path)
	if err != nil {
		return false, err
	}
	return got == item.MD5, nil
}

// ReconcileExistingFile observes the destination against the item and returns
// the authoritative action plus the chunk index a resume would start at (only
// meaningful for DecisionResume). The file is not modified; an observation
// failure comes back as a non-nil error and never as a decision.
func ReconcileExistingFile(item model.GalaxyDepotItem, path string) (Decision, int, error) {
	if item.TotalSize == 0 {
		// Empty item: the destination must end up as an empty file. Absent or
		// already empty ⇒ the transfer's empty-file creation covers it;
		// anything else ⇒ replace (truncate).
		fi, err := os.Stat(path)
		if errors.Is(err, fs.ErrNotExist) {
			return DecisionDownload, 0, nil
		}
		if err != nil {
			return DecisionDownload, 0, err
		}
		if fi.Size() == 0 {
			return DecisionDownload, 0, nil
		}
		return DecisionReplace, 0, nil
	}

	fi, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return DecisionDownload, 0, nil
	}
	if err != nil {
		return DecisionDownload, 0, err
	}
	size := fi.Size()

	if size > int64(item.TotalSize) {
		return DecisionReplace, 0, nil
	}
	if size == int64(item.TotalSize) {
		// Same observation IsComplete makes; reusing it keeps one definition
		// of "this destination already satisfies the item".
		complete, err := IsComplete(item, path)
		if err != nil {
			return DecisionDownload, 0, err
		}
		if complete {
			return DecisionSkip, 0, nil
		}
		return DecisionReplace, 0, nil
	}

	// size < TotalSize: resumable only when the size lands exactly on an
	// uncompressed chunk boundary AND the chunk before that boundary matches
	// its uncompressed md5. A zero size means "start from chunk 0", which is a
	// plain download rather than a dangerous resume (review RES1 v3 §15).
	for n, chunk := range item.Chunks {
		if int64(chunk.Offset) != size {
			continue
		}
		if n == 0 {
			return DecisionDownload, 0, nil
		}
		prev := item.Chunks[n-1]
		match, err := chunkMD5(path, int64(prev.Offset), int64(prev.Size), prev.MD5)
		if err != nil {
			return DecisionDownload, 0, err
		}
		if match {
			return DecisionResume, n, nil
		}
		return DecisionReplace, 0, nil
	}
	return DecisionReplace, 0, nil
}

// fileMD5 reports whether the file's whole content hashes to want. The size is
// checked first so a mismatch never costs a full read.
func fileMD5(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// chunkMD5 reports whether the uncompressed bytes of the file at
// [offset, offset+size) hash to want.
func chunkMD5(path string, offset, size int64, want string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return false, err
	}
	h := md5.New()
	if _, err := io.CopyN(h, f, size); err != nil {
		return false, err
	}
	return hex.EncodeToString(h.Sum(nil)) == want, nil
}
