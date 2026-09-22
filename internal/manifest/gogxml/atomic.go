package gogxml

import (
	"os"
	"path/filepath"
)

// WriteAtomic writes data to path using a sibling temporary file.
//
// If the operation fails before publication, the temporary file is removed
// and the existing destination file (if any) is unmodified.
// Publication uses platform-available replacement semantics.
func WriteAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanTmp := true
	defer func() {
		if cleanTmp {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	if perm != 0 {
		_ = os.Chmod(tmpName, perm)
	}

	// Publish via Rename. Go's Rename replaces an existing destination on POSIX
	// and on Windows (MoveFileEx with MOVEFILE_REPLACE_EXISTING), so no
	// remove-first dance is needed — and none is taken: if the rename fails
	// for any platform reason, the destination is left exactly as it was and
	// only the temporary file is cleaned up. A publish failure must never
	// destroy a manifest that was already in place.
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}

	cleanTmp = false
	return nil
}
