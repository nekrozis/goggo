package reconcile

import (
	"crypto/md5"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/nekrozis/goggo/internal/model"
)

func md5hex(data []byte) string {
	sum := md5.Sum(data)
	return hex.EncodeToString(sum[:])
}

// TestClassifyExistingFileStates walks the four facts over a real file: the
// size is compared before the content, a path in another shape is "not
// downloaded", and a zero-size item needs no special case.
func TestClassifyExistingFileStates(t *testing.T) {
	content := []byte("the expected bytes")
	item := model.GalaxyDepotItem{Path: "game/file.bin", TotalSize: uint64(len(content)), MD5: md5hex(content)}

	dir := t.TempDir()
	path := filepath.Join(dir, "file.bin")

	write := func(t *testing.T, body []byte) {
		t.Helper()
		if err := os.WriteFile(path, body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	remove := func(t *testing.T) {
		t.Helper()
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	classify := func(t *testing.T) FileStatus {
		t.Helper()
		got, err := ClassifyExistingFile(item, path)
		if err != nil {
			t.Fatalf("ClassifyExistingFile: %v", err)
		}
		return got
	}

	if got := classify(t); got != StatusND {
		t.Errorf("absent = %v, want %v", got, StatusND)
	}
	write(t, content)
	if got := classify(t); got != StatusOK {
		t.Errorf("exact file = %v, want %v", got, StatusOK)
	}
	// Same size, different bytes: the version differs, the download finished.
	corrupt := append([]byte(nil), content...)
	corrupt[0] = 'X'
	write(t, corrupt)
	if got := classify(t); got != StatusMD5 {
		t.Errorf("same size, other content = %v, want %v", got, StatusMD5)
	}
	// A size mismatch in either direction is FS, and it is answered without
	// reading the file.
	write(t, content[:len(content)-1])
	if got := classify(t); got != StatusFS {
		t.Errorf("truncated = %v, want %v", got, StatusFS)
	}
	write(t, append(append([]byte(nil), content...), 'X'))
	if got := classify(t); got != StatusFS {
		t.Errorf("oversize = %v, want %v", got, StatusFS)
	}

	// A path that exists in another shape is not the expected regular file;
	// it folds into ND.
	remove(t)
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := classify(t); got != StatusND {
		t.Errorf("directory in place = %v, want %v", got, StatusND)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	// A zero-size item: absent is ND, an empty file is OK, anything else is FS.
	empty := model.GalaxyDepotItem{Path: "game/empty.bin", TotalSize: 0, MD5: md5hex(nil)}
	if got, err := ClassifyExistingFile(empty, path); err != nil || got != StatusND {
		t.Errorf("absent empty item = (%v, %v), want (%v, nil)", got, err, StatusND)
	}
	write(t, nil)
	if got, err := ClassifyExistingFile(empty, path); err != nil || got != StatusOK {
		t.Errorf("empty file for an empty item = (%v, %v), want (%v, nil)", got, err, StatusOK)
	}
	write(t, []byte("x"))
	if got, err := ClassifyExistingFile(empty, path); err != nil || got != StatusFS {
		t.Errorf("non-empty file for an empty item = (%v, %v), want (%v, nil)", got, err, StatusFS)
	}
}

// TestClassifyExistingFileObservationFailure locks that an unreadable path is
// an error and never a fact: what cannot be observed must not be reported as
// absent, let alone as fine.
//
// The injection is a NUL byte in the path, which the filesystem rejects on every
// platform — the project's environment-independent failure injection (a
// permission- or lock-based one asserts the environment, not the code). The
// assertion follows the same rule: check the error class and the preserved path,
// never the rendered text.
func TestClassifyExistingFileObservationFailure(t *testing.T) {
	item := model.GalaxyDepotItem{Path: "game/file.bin", TotalSize: 12, MD5: "whatever"}

	status, err := ClassifyExistingFile(item, "x\x00y")
	if err == nil {
		t.Fatal("an unreadable path must return an error, not a status")
	}
	if status != StatusUnset {
		t.Errorf("status = %v, want the unset value: no fact exists for an unreadable file", status)
	}
	if errors.Is(err, fs.ErrNotExist) {
		t.Error("the failure must not read as \"the file is absent\"")
	}
	var pathErr *fs.PathError
	if !errors.As(err, &pathErr) {
		t.Fatalf("err = %v, want a *fs.PathError", err)
	}
	if pathErr.Path != "x\x00y" {
		t.Errorf("PathError.Path = %q, want the path verbatim", pathErr.Path)
	}
}

// TestFileStatusCodes locks the four codes the status report prints and the
// fact that an unset status does not print as OK.
func TestFileStatusCodes(t *testing.T) {
	want := map[FileStatus]string{
		StatusOK:    "OK",
		StatusND:    "ND",
		StatusMD5:   "MD5",
		StatusFS:    "FS",
		StatusUnset: "UNSET",
	}
	for status, code := range want {
		if got := status.String(); got != code {
			t.Errorf("FileStatus(%d).String() = %q, want %q", status, got, code)
		}
	}
}

// TestIsCompleteZeroSizeItem locks the three-state zero-size rule: missing ⇒
// false, empty ⇒ true, non-empty ⇒ false. A plan must never mark an absent empty
// file as skipped.
func TestIsCompleteZeroSizeItem(t *testing.T) {
	item := model.GalaxyDepotItem{Path: "game/empty.bin", TotalSize: 0, MD5: "d41d8cd98f00b204e9800998ecf8427e"}
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.bin")

	if ok, err := IsComplete(item, path); ok || err != nil {
		t.Errorf("missing file: (%v, %v), want (false, nil)", ok, err)
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if ok, err := IsComplete(item, path); err != nil || !ok {
		t.Errorf("empty file: (%v, %v), want (true, nil)", ok, err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ok, err := IsComplete(item, path); err != nil || ok {
		t.Errorf("non-empty file: (%v, %v), want (false, nil)", ok, err)
	}
}

// TestReconcileExistingFileStates walks the classification state machine over
// a real file: absent / exact boundary with matching previous chunk / oversize.
func TestReconcileExistingFileStates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.bin")

	chunkSize := 1000
	var content []byte
	var chunks []model.GalaxyDepotItemChunk
	for i := 0; i < 3; i++ {
		part := make([]byte, chunkSize)
		for j := range part {
			part[j] = byte('a' + i)
		}
		content = append(content, part...)
		chunks = append(chunks, model.GalaxyDepotItemChunk{
			CompressedMD5:  "unused",
			MD5:            md5hex(part),
			CompressedSize: uint64(chunkSize),
			Size:           uint64(chunkSize),
			Offset:         uint64(i * chunkSize),
		})
	}
	item := model.GalaxyDepotItem{
		Path:                "game/file.bin",
		TotalSize:           uint64(len(content)),
		TotalCompressedSize: uint64(len(content)),
		Chunks:              chunks,
		MD5:                 md5hex(content),
	}

	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	decision, start, err := ReconcileExistingFile(item, path)
	if err != nil {
		t.Fatalf("ReconcileExistingFile: %v", err)
	}
	if decision != DecisionSkip {
		t.Errorf("complete file: decision = %v (start %d), want skip", decision, start)
	}

	// Truncated to an exact chunk boundary (2000 of 3000 bytes): resumable at
	// chunk 2 — the chunk before that boundary must match its uncompressed md5.
	if err := os.WriteFile(path, content[:2000], 0o644); err != nil {
		t.Fatal(err)
	}
	decision, start, err = ReconcileExistingFile(item, path)
	if err != nil {
		t.Fatalf("ReconcileExistingFile: %v", err)
	}
	if decision != DecisionResume || start != 2 {
		t.Errorf("boundary partial: decision = %v (start %d), want resume at 2", decision, start)
	}

	// Oversized: replace.
	if err := os.WriteFile(path, append(content, []byte("extra")...), 0o644); err != nil {
		t.Fatal(err)
	}
	if decision, _, _ = ReconcileExistingFile(item, path); decision != DecisionReplace {
		t.Errorf("oversized: decision = %v, want replace", decision)
	}

	// Missing: download.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if decision, _, _ = ReconcileExistingFile(item, path); decision != DecisionDownload {
		t.Errorf("missing: decision = %v, want download", decision)
	}
}

// TestReconcilePartialNonBoundaryAndCorruptBoundary covers the two unsafe
// partial shapes: a size that lands between chunk boundaries, and a boundary
// whose preceding chunk no longer matches its uncompressed md5. Both must be
// replaced rather than resumed.
func TestReconcilePartialNonBoundaryAndCorruptBoundary(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.bin")

	chunkSize := 1000
	var content []byte
	var chunks []model.GalaxyDepotItemChunk
	for i := 0; i < 3; i++ {
		part := make([]byte, chunkSize)
		for j := range part {
			part[j] = byte('a' + i)
		}
		content = append(content, part...)
		chunks = append(chunks, model.GalaxyDepotItemChunk{
			MD5:            md5hex(part),
			CompressedSize: uint64(chunkSize),
			Size:           uint64(chunkSize),
			Offset:         uint64(i * chunkSize),
		})
	}
	item := model.GalaxyDepotItem{
		Path:                "game/file.bin",
		TotalSize:           uint64(len(content)),
		TotalCompressedSize: uint64(len(content)),
		Chunks:              chunks,
	}

	// 1500 bytes: not a chunk boundary.
	if err := os.WriteFile(path, content[:1500], 0o644); err != nil {
		t.Fatal(err)
	}
	if decision, _, _ := ReconcileExistingFile(item, path); decision != DecisionReplace {
		t.Errorf("non-boundary partial: decision = %v, want replace", decision)
	}

	// Exactly one chunk but its content no longer matches the first chunk's
	// uncompressed md5: the boundary is invalid.
	if err := os.WriteFile(path, []byte("corrupted content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if decision, _, _ := ReconcileExistingFile(item, path); decision != DecisionReplace {
		t.Errorf("corrupt boundary: decision = %v, want replace", decision)
	}
}

// TestClassifyExistingFile_EmptyMD5 verifies that an item without a declared md5
// (empty string, e.g. HoMM3 savegames.txt or undeclared assets) is satisfied by
// its size alone: zero-size items and non-zero-size items both classify as OK
// without reading the file or producing a false MD5 mismatch.
func TestClassifyExistingFile_EmptyMD5(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.bin")

	// Zero-size item with empty MD5.
	emptyItem := model.GalaxyDepotItem{Path: "game/savegames.txt", TotalSize: 0, MD5: ""}
	if got, err := ClassifyExistingFile(emptyItem, path); err != nil || got != StatusND {
		t.Errorf("absent empty item = (%v, %v), want (%v, nil)", got, err, StatusND)
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := ClassifyExistingFile(emptyItem, path); err != nil || got != StatusOK {
		t.Errorf("empty file with empty MD5 = (%v, %v), want (%v, nil)", got, err, StatusOK)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := ClassifyExistingFile(emptyItem, path); err != nil || got != StatusFS {
		t.Errorf("non-empty file for empty item = (%v, %v), want (%v, nil)", got, err, StatusFS)
	}

	// Non-zero item with empty MD5.
	body := []byte("hello world, size 24 bytes!")
	item := model.GalaxyDepotItem{Path: "game/data.bin", TotalSize: uint64(len(body)), MD5: ""}
	if got, err := ClassifyExistingFile(item, path); err != nil || got != StatusFS {
		t.Errorf("truncated item = (%v, %v), want (%v, nil)", got, err, StatusFS)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := ClassifyExistingFile(item, path); err != nil || got != StatusOK {
		t.Errorf("exact size with empty MD5 = (%v, %v), want (%v, nil)", got, err, StatusOK)
	}
	if err := os.WriteFile(path, append(body, '!'), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := ClassifyExistingFile(item, path); err != nil || got != StatusFS {
		t.Errorf("oversize item = (%v, %v), want (%v, nil)", got, err, StatusFS)
	}

	// Directory occupant.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := ClassifyExistingFile(item, path); err != nil || got != StatusND {
		t.Errorf("directory for regular item = (%v, %v), want (%v, nil)", got, err, StatusND)
	}
}

// TestIsComplete_EmptyMD5 verifies that IsComplete returns true when the size
// matches for empty-MD5 items, and false when size or mode differs.
func TestIsComplete_EmptyMD5(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.bin")

	emptyItem := model.GalaxyDepotItem{Path: "game/empty.txt", TotalSize: 0, MD5: ""}
	if ok, err := IsComplete(emptyItem, path); err != nil || ok {
		t.Errorf("absent empty = (%v, %v), want (false, nil)", ok, err)
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if ok, err := IsComplete(emptyItem, path); err != nil || !ok {
		t.Errorf("empty file = (%v, %v), want (true, nil)", ok, err)
	}
	if err := os.WriteFile(path, []byte("non-empty"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ok, err := IsComplete(emptyItem, path); err != nil || ok {
		t.Errorf("non-empty for 0-size = (%v, %v), want (false, nil)", ok, err)
	}

	nonEmptyItem := model.GalaxyDepotItem{Path: "game/asset.dat", TotalSize: 10, MD5: ""}
	if ok, err := IsComplete(nonEmptyItem, path); err != nil || ok {
		t.Errorf("size 9 != 10: (%v, %v), want (false, nil)", ok, err)
	}
	if err := os.WriteFile(path, []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ok, err := IsComplete(nonEmptyItem, path); err != nil || !ok {
		t.Errorf("exact size with empty MD5: (%v, %v), want (true, nil)", ok, err)
	}

	// Directory occupying path.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if ok, err := IsComplete(nonEmptyItem, path); err != nil || ok {
		t.Errorf("directory: (%v, %v), want (false, nil)", ok, err)
	}
}

// TestReconcileExistingFile_EmptyMD5 verifies that ReconcileExistingFile returns
// DecisionSkip for non-zero items matching size with empty MD5, and DecisionReplace
// for directories or mismatched sizes.
func TestReconcileExistingFile_EmptyMD5(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.bin")

	content := []byte("0123456789")
	item := model.GalaxyDepotItem{
		Path:      "game/file.bin",
		TotalSize: uint64(len(content)),
		MD5:       "",
	}

	// Exact size: DecisionSkip.
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	dec, start, err := ReconcileExistingFile(item, path)
	if err != nil || dec != DecisionSkip {
		t.Fatalf("exact size empty MD5: (%v, %d, %v), want (DecisionSkip, 0, nil)", dec, start, err)
	}

	// Truncated non-boundary: DecisionReplace.
	if err := os.WriteFile(path, content[:5], 0o644); err != nil {
		t.Fatal(err)
	}
	dec, _, err = ReconcileExistingFile(item, path)
	if err != nil || dec != DecisionReplace {
		t.Errorf("truncated: (%v, %v), want (DecisionReplace, nil)", dec, err)
	}

	// Directory occupant: strictly DecisionReplace.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	dec, _, err = ReconcileExistingFile(item, path)
	if err != nil || dec != DecisionReplace {
		t.Errorf("directory occupant: (%v, %v), want (DecisionReplace, nil)", dec, err)
	}
}

// TestIsCompleteAndReconcileObservationFailure locks the invariant that
// observation failures on unreadable paths return an error and never a decision
// or false completeness.
func TestIsCompleteAndReconcileObservationFailure(t *testing.T) {
	item := model.GalaxyDepotItem{Path: "game/file.bin", TotalSize: 12, MD5: ""}
	if _, err := IsComplete(item, "x\x00y"); err == nil {
		t.Fatal("IsComplete on unobservable path must return error")
	}
	if _, _, err := ReconcileExistingFile(item, "x\x00y"); err == nil {
		t.Fatal("ReconcileExistingFile on unobservable path must return error")
	}
}
