package reconcile

import (
	"crypto/md5"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/nekrozis/goggo/internal/model"
)

func md5hex(data []byte) string {
	sum := md5.Sum(data)
	return hex.EncodeToString(sum[:])
}

// TestIsCompleteZeroSizeItem locks the three-state zero-size rule (review
// RES1 v3 §4): missing ⇒ false, empty ⇒ true, non-empty ⇒ false. A plan must
// never mark an absent empty file as skipped.
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
