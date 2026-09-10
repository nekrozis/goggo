package model

import "testing"

// TestFileTaskConstruction locks that a task carries the depot entry and its
// resolved destination, and that the entry's galaxy-specific fields stay
// reachable through Item — the transfer layer reads them without importing
// galaxy.
func TestFileTaskConstruction(t *testing.T) {
	item := GalaxyDepotItem{
		Chunks: []GalaxyDepotItemChunk{{CompressedMD5: "ab", MD5: "cd"}},
		Path:   "game/data.bin", ProductID: "1495134320",
		TotalCompressedSize: 100, TotalSize: 200,
		SFCOffset: 7, SFCSize: 9, IsInSFC: true,
		IsDependency: true, IsSmallFilesContainer: false,
	}
	task := FileTask{Item: item, Destination: `/install/game/data.bin`}

	if task.Item.Path != "game/data.bin" || task.Item.ProductID != "1495134320" {
		t.Errorf("item path/product = %q/%q", task.Item.Path, task.Item.ProductID)
	}
	if len(task.Item.Chunks) != 1 || task.Item.Chunks[0].CompressedMD5 != "ab" {
		t.Errorf("item chunks = %+v", task.Item.Chunks)
	}
	if !task.Item.IsDependency || task.Item.IsSmallFilesContainer {
		t.Errorf("item flags = %+v", task.Item)
	}
	if !task.Item.IsInSFC || task.Item.SFCOffset != 7 || task.Item.SFCSize != 9 {
		t.Errorf("item SFC = %+v", task.Item)
	}
	if task.Destination != "/install/game/data.bin" {
		t.Errorf("destination = %q", task.Destination)
	}
}
