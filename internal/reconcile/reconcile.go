// Package reconcile observes local files against a GalaxyDepotItem and decides
// how an install must treat them: skip, download, replace or resume.
//
// It is observation-only and never modifies the filesystem. Its decision is
// authoritative for the caller; a plan's classification is an optimisation, never
// the correctness boundary (decisions D13-D19, D42-D44).
//
// Resume semantics (decisions D6/D15): a partial file is resumable only when its
// size lands exactly on an uncompressed chunk boundary AND the chunk before that
// boundary matches its uncompressed md5; anything else is replaced. Whole-file
// correctness is backstopped by the Item.MD5 check on the next reconcile, never
// by a full-prefix rehash.
//
// ClassifyExistingFile answers the other question — not "what must an install do"
// but "what is there" (OK/ND/MD5/FS) — and returns a fact, never an action.
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

// FileStatus is the FACT a read-only observation finds about one destination:
// not an action (that is Decision), just what is there. It exists because the
// two questions are different — DecisionReplace covers both "the hash differs"
// and "the size differs", while a verification has to report which one it saw.
//
// The four codes are the vocabulary of the status report. The
// zero value is deliberately NOT StatusOK: a value that was never observed must
// not read as a healthy file (the same rule the CLI's session class follows).
type FileStatus uint8

const (
	// StatusUnset is what an unobserved file has. ClassifyExistingFile never
	// returns it; it is the zero value so that a fact built without an
	// observation cannot pass for OK.
	StatusUnset FileStatus = iota

	// StatusOK means the size and the whole-file hash both match the item.
	StatusOK

	// StatusND means the expected regular file is not there. A path that
	// exists in another shape (a directory) takes this answer too.
	StatusND

	// StatusMD5 means the size matches but the content hash does not: a
	// different version of the same asset.
	StatusMD5

	// StatusFS means the size does not match, in either direction: a download
	// that did not finish.
	StatusFS
)

// String returns the code the status report prints.
func (s FileStatus) String() string {
	switch s {
	case StatusOK:
		return "OK"
	case StatusND:
		return "ND"
	case StatusMD5:
		return "MD5"
	case StatusFS:
		return "FS"
	}
	return "UNSET"
}

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
// skipped.
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
	// plain download rather than a dangerous resume.
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

// ClassifyExistingFile reports the observed fact about the destination against
// the item, without modifying anything. It answers the question a verification
// asks, which is not the question ReconcileExistingFile answers: the latter
// decides what an install must DO (and folds "hash differs" and "size differs"
// into DecisionReplace), this one reports WHAT IS THERE.
//
// The order of the checks gives the four codes their meaning:
//
//	absent (or not a regular file) → StatusND
//	size differs → StatusFS (a size mismatch outranks a hash mismatch: the
//	               comparison never reads the file)
//	hash differs → StatusMD5
//	otherwise → StatusOK
//
// A zero-size item needs no special case: it is OK when an empty file is there,
// ND when the path is absent, and FS when something non-empty occupies it. An
// observation failure (stat/open/read) comes back as a non-nil error and never
// as a status: "the file could not be read" is not a fact about its content
// (D43).
func ClassifyExistingFile(item model.GalaxyDepotItem, path string) (FileStatus, error) {
	fi, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return StatusND, nil
	}
	if err != nil {
		return StatusUnset, err
	}
	if !fi.Mode().IsRegular() {
		return StatusND, nil
	}
	if fi.Size() != int64(item.TotalSize) {
		return StatusFS, nil
	}
	got, err := fileMD5(path)
	if err != nil {
		return StatusUnset, err
	}
	if got != item.MD5 {
		return StatusMD5, nil
	}
	return StatusOK, nil
}

// fileMD5 returns the hex md5 of the file's whole content.
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
