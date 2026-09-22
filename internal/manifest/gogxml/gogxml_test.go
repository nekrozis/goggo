package gogxml

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// md5Of returns the 32-hex md5 string of data.
func md5Of(data []byte) string {
	sum := md5.Sum(data)
	return hex.EncodeToString(sum[:])
}

// T-XML-1: TestGogXMLParseAndRoundtrip verifies that valid GOG XML documents
// are losslessly parsed and can round-trip with formatting whitespace differences ignored.
func TestGogXMLParseAndRoundtrip(t *testing.T) {
	xmlText := `<?xml version="1.0" encoding="UTF-8"?>
<file name="setup_homm3.exe" chunks="2" total_size="15000000" md5="5d41402abc4b2a76b9719d911017c592">
  <chunk id="0" from="0" to="10485759" method="md5">b10a8db164e0754105b7a99be72e3fe5</chunk>
  <chunk id="1" from="10485760" to="14999999" method="md5">e2fc714c4727ee9395f324cd2e7f331f</chunk>
</file>`

	parsed, err := Parse(strings.NewReader(xmlText))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if parsed.Name != "setup_homm3.exe" {
		t.Errorf("Name = %q, want %q", parsed.Name, "setup_homm3.exe")
	}
	if parsed.Chunks != 2 || len(parsed.ChunkList) != 2 {
		t.Errorf("Chunks = %d, chunk count = %d, want 2", parsed.Chunks, len(parsed.ChunkList))
	}
	if parsed.TotalSize != 15000000 {
		t.Errorf("TotalSize = %d, want 15000000", parsed.TotalSize)
	}
	if parsed.MD5 != "5d41402abc4b2a76b9719d911017c592" {
		t.Errorf("MD5 = %q, want %q", parsed.MD5, "5d41402abc4b2a76b9719d911017c592")
	}
	if parsed.ChunkList[0].ID != 0 || parsed.ChunkList[0].From != 0 || parsed.ChunkList[0].To != 10485759 {
		t.Errorf("Chunk 0 range = [%d, %d], want [0, 10485759]", parsed.ChunkList[0].From, parsed.ChunkList[0].To)
	}
	if parsed.ChunkList[1].ID != 1 || parsed.ChunkList[1].From != 10485760 || parsed.ChunkList[1].To != 14999999 {
		t.Errorf("Chunk 1 range = [%d, %d], want [10485760, 14999999]", parsed.ChunkList[1].From, parsed.ChunkList[1].To)
	}

	marshaled, err := Marshal(parsed)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	reparsed, err := Parse(bytes.NewReader(marshaled))
	if err != nil {
		t.Fatalf("Re-parse failed: %v", err)
	}
	if reparsed.Name != parsed.Name || reparsed.TotalSize != parsed.TotalSize || reparsed.MD5 != parsed.MD5 {
		t.Errorf("Roundtrip mismatch: got %+v, want %+v", reparsed, parsed)
	}
}

// T-XML-2: TestGogXMLSemanticValidation12Rules verifies that all 12 semantic rules
// are individually enforced and reject invalid manifests.
func TestGogXMLSemanticValidation12Rules(t *testing.T) {
	validManifest := func() *FileXML {
		return &FileXML{
			Name:      "test.bin",
			Chunks:    2,
			TotalSize: 200,
			MD5:       "0123456789abcdef0123456789abcdef",
			ChunkList: []ChunkXML{
				{ID: 0, From: 0, To: 99, Method: "md5", Hash: "0123456789abcdef0123456789abcdef"},
				{ID: 1, From: 100, To: 199, Method: "md5", Hash: "0123456789abcdef0123456789abcdef"},
			},
		}
	}

	cases := []struct {
		name     string
		mutate   func(*FileXML)
		wantRule int
	}{
		{
			name: "Rule 1: negative total size",
			mutate: func(f *FileXML) {
				f.TotalSize = -1
			},
			wantRule: 1,
		},
		{
			name: "Rule 2: chunks count attribute mismatch",
			mutate: func(f *FileXML) {
				f.Chunks = 3
			},
			wantRule: 2,
		},
		{
			name: "Rule 12: empty file MD5 is forbidden",
			mutate: func(f *FileXML) {
				f.MD5 = ""
			},
			wantRule: 12,
		},
		{
			name: "Rule 12: non-hex file MD5",
			mutate: func(f *FileXML) {
				f.MD5 = "invalid-md5-not-32-hex-chars!!!"
			},
			wantRule: 12,
		},
		{
			name: "Rule 3: empty file with chunks",
			mutate: func(f *FileXML) {
				f.TotalSize = 0
				f.Chunks = 1
				f.ChunkList = []ChunkXML{{ID: 0, From: 0, To: 0, Method: "md5", Hash: "0123456789abcdef0123456789abcdef"}}
				f.MD5 = emptyFileMD5
			},
			wantRule: 3,
		},
		{
			name: "Rule 3: empty file with wrong MD5",
			mutate: func(f *FileXML) {
				f.TotalSize = 0
				f.Chunks = 0
				f.ChunkList = []ChunkXML{}
				f.MD5 = "0123456789abcdef0123456789abcdef"
			},
			wantRule: 3,
		},
		{
			name: "Rule 4: non-empty file with 0 chunks",
			mutate: func(f *FileXML) {
				f.Chunks = 0
				f.ChunkList = []ChunkXML{}
			},
			wantRule: 4,
		},
		{
			name: "Rule 5: chunk ID not matching index",
			mutate: func(f *FileXML) {
				f.ChunkList[1].ID = 5
			},
			wantRule: 5,
		},
		{
			name: "Rule 6: chunk from > to",
			mutate: func(f *FileXML) {
				f.ChunkList[0].From = 100
				f.ChunkList[0].To = 50
			},
			wantRule: 6,
		},
		{
			name: "Rule 7: first chunk not starting at 0",
			mutate: func(f *FileXML) {
				f.ChunkList[0].From = 10
			},
			wantRule: 7,
		},
		{
			name: "Rule 8: gap between chunks",
			mutate: func(f *FileXML) {
				f.ChunkList[1].From = 105 // gap 100..104
			},
			wantRule: 8,
		},
		{
			name: "Rule 8: overlap between chunks",
			mutate: func(f *FileXML) {
				f.ChunkList[1].From = 90 // overlaps chunk 0
			},
			wantRule: 8,
		},
		{
			name: "Rule 9: last chunk not covering TotalSize - 1",
			mutate: func(f *FileXML) {
				f.ChunkList[1].To = 180 // expected 199
			},
			wantRule: 9,
		},
		{
			name: "Rule 10: unsupported method",
			mutate: func(f *FileXML) {
				f.ChunkList[0].Method = "sha256"
			},
			wantRule: 10,
		},
		{
			name: "Rule 11: invalid chunk hash",
			mutate: func(f *FileXML) {
				f.ChunkList[0].Hash = "short"
			},
			wantRule: 11,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := validManifest()
			tc.mutate(m)
			err := m.Validate()
			if err == nil {
				t.Fatalf("expected validation error for %s, got nil", tc.name)
			}
			// The taxonomy, not the wording: a validation failure must be a
			// *SemanticValidationError carrying the rule number.
			var semErr *SemanticValidationError
			if !errors.As(err, &semErr) {
				t.Fatalf("error = %v (%T), want *SemanticValidationError", err, err)
			}
			if semErr.Rule != tc.wantRule {
				t.Errorf("error rule = %d, want %d (%v)", semErr.Rule, tc.wantRule, err)
			}
		})
	}
}

// T-XML-3: TestGogXMLChunkPlanSlicing tests the pure math chunk slicing algorithm.
func TestGogXMLChunkPlanSlicing(t *testing.T) {
	// 0 bytes
	plans0, err := SplitChunks(0, 10485760)
	if err != nil || len(plans0) != 0 {
		t.Errorf("SplitChunks(0) = (%v, %v), want ([], nil)", plans0, err)
	}

	// 5 MiB with 10 MiB chunk size: 1 chunk [0, 5242879]
	size5MB := int64(5 * 1024 * 1024)
	chunkSize := int64(10 * 1024 * 1024)
	plans5, err := SplitChunks(size5MB, chunkSize)
	if err != nil || len(plans5) != 1 {
		t.Fatalf("SplitChunks(5MiB) len = %d, want 1", len(plans5))
	}
	if plans5[0].From != 0 || plans5[0].To != size5MB-1 {
		t.Errorf("Chunk 0 = [%d, %d], want [0, %d]", plans5[0].From, plans5[0].To, size5MB-1)
	}

	// 25 MiB with 10 MiB chunk size: 3 chunks
	size25MB := int64(25 * 1024 * 1024)
	plans25, err := SplitChunks(size25MB, chunkSize)
	if err != nil || len(plans25) != 3 {
		t.Fatalf("SplitChunks(25MiB) len = %d, want 3", len(plans25))
	}
	if plans25[0].From != 0 || plans25[0].To != 10485759 {
		t.Errorf("Chunk 0 = [%d, %d], want [0, 10485759]", plans25[0].From, plans25[0].To)
	}
	if plans25[1].From != 10485760 || plans25[1].To != 20971519 {
		t.Errorf("Chunk 1 = [%d, %d], want [10485760, 20971519]", plans25[1].From, plans25[1].To)
	}
	if plans25[2].From != 20971520 || plans25[2].To != size25MB-1 {
		t.Errorf("Chunk 2 = [%d, %d], want [20971520, %d]", plans25[2].From, plans25[2].To, size25MB-1)
	}
}

// T-XML-4: TestGogXMLGenerateStreamLengthGuard verifies that Generate rejects readers
// that yield fewer or more bytes than the declared size.
func TestGogXMLGenerateStreamLengthGuard(t *testing.T) {
	// Reader yields fewer bytes than declared
	shortData := []byte("short data")
	declaredSize := int64(len(shortData) + 100)
	_, err := Generate(bytes.NewReader(shortData), declaredSize, 1024, "test.bin")
	if err != ErrStreamLengthMismatch {
		t.Errorf("short stream: err = %v, want ErrStreamLengthMismatch", err)
	}

	// Reader yields more bytes than declared
	longData := []byte("excess data beyond declared size")
	_, err = Generate(bytes.NewReader(longData), 10, 1024, "test.bin")
	if err != ErrStreamLengthMismatch {
		t.Errorf("long stream: err = %v, want ErrStreamLengthMismatch", err)
	}
}

// T-XML-5: TestGogXMLVerifyDetectsTailExcess locks that if actual size > manifest.TotalSize,
// Verify immediately returns StatusSizeMismatch without reading chunks and leaves actual_md5 empty.
func TestGogXMLVerifyDetectsTailExcess(t *testing.T) {
	content := []byte("Hello, this is standard chunked content.")
	manifest, err := Generate(bytes.NewReader(content), int64(len(content)), 10, "data.bin")
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// Actual file is 1 byte longer than manifest.TotalSize
	extendedContent := append(content, 'X')
	report, err := Verify(bytes.NewReader(extendedContent), int64(len(extendedContent)), manifest)
	if err != nil {
		t.Fatalf("Verify failed: %v", err)
	}

	if report.Status != StatusSizeMismatch {
		t.Errorf("report.Status = %q, want %q", report.Status, StatusSizeMismatch)
	}
	if report.ActualMD5 != "" {
		t.Errorf("report.ActualMD5 = %q, want empty string for size mismatch", report.ActualMD5)
	}
	if len(report.Chunks) != 0 {
		t.Errorf("report.Chunks len = %d, want 0 chunks read on size mismatch", len(report.Chunks))
	}
}

// T-XML-6: TestGogXMLVerifyDetectsCorruptChunkAndFullMD5 verifies that Verify detects
// corrupt chunks, counts all corrupt chunks across the entire file without aborting early,
// and records expected vs actual hashes.
func TestGogXMLVerifyDetectsCorruptChunkAndFullMD5(t *testing.T) {
	// 30 bytes, chunk size 10 -> 3 chunks
	content := []byte("0123456789abcdefghij0123456789")
	manifest, err := Generate(bytes.NewReader(content), int64(len(content)), 10, "test.bin")
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// Corrupt chunk 0 and chunk 2
	tampered := make([]byte, len(content))
	copy(tampered, content)
	tampered[0] = 'X'
	tampered[20] = 'Y'

	report, err := Verify(bytes.NewReader(tampered), int64(len(tampered)), manifest)
	if err != nil {
		t.Fatalf("Verify failed: %v", err)
	}

	if report.Status != StatusChunkMismatch {
		t.Errorf("report.Status = %q, want %q", report.Status, StatusChunkMismatch)
	}
	if report.CorruptChunks != 2 {
		t.Errorf("report.CorruptChunks = %d, want 2", report.CorruptChunks)
	}
	if len(report.Chunks) != 3 {
		t.Fatalf("report.Chunks len = %d, want 3", len(report.Chunks))
	}
	if report.Chunks[0].OK {
		t.Errorf("chunk 0 OK = true, want false")
	}
	if !report.Chunks[1].OK {
		t.Errorf("chunk 1 OK = false, want true")
	}
	if report.Chunks[2].OK {
		t.Errorf("chunk 2 OK = true, want false")
	}
	if report.FileMD5Match {
		t.Errorf("report.FileMD5Match = true, want false")
	}
}

// TestGogXMLEmptyFile verifies 0-byte file generation and verification.
func TestGogXMLEmptyFile(t *testing.T) {
	manifest, err := Generate(strings.NewReader(""), 0, 1024, "empty.dat")
	if err != nil {
		t.Fatalf("Generate empty file failed: %v", err)
	}
	if manifest.Chunks != 0 || manifest.TotalSize != 0 {
		t.Errorf("manifest = %+v, want chunks=0, size=0", manifest)
	}
	if manifest.MD5 != emptyFileMD5 {
		t.Errorf("MD5 = %q, want %q", manifest.MD5, emptyFileMD5)
	}

	report, err := Verify(strings.NewReader(""), 0, manifest)
	if err != nil {
		t.Fatalf("Verify empty file failed: %v", err)
	}
	if report.Status != StatusChunkVerified {
		t.Errorf("report.Status = %q, want %q", report.Status, StatusChunkVerified)
	}
}

// TestWriteAtomic tests atomic file write, temporary file cleanup, and replacement.
func TestWriteAtomic(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "manifest.xml")

	data1 := []byte("<file name=\"v1\"/>")
	if err := WriteAtomic(target, data1, 0o644); err != nil {
		t.Fatalf("WriteAtomic v1 failed: %v", err)
	}

	read1, err := os.ReadFile(target)
	if err != nil || string(read1) != string(data1) {
		t.Fatalf("read1 = %q, %v", read1, err)
	}

	// Overwrite existing target
	data2 := []byte("<file name=\"v2\"/>")
	if err := WriteAtomic(target, data2, 0o644); err != nil {
		t.Fatalf("WriteAtomic v2 overwrite failed: %v", err)
	}

	read2, err := os.ReadFile(target)
	if err != nil || string(read2) != string(data2) {
		t.Fatalf("read2 = %q, %v", read2, err)
	}

	// Confirm no temporary files left in directory
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "manifest.xml" {
		t.Errorf("unexpected files in dir: %v", entries)
	}
}

// TestWriteAtomicFailureKeepsTarget pins the publish-failure contract: when the
// rename cannot publish, the destination that was already in place survives
// byte-for-byte and no temporary file is left behind. The destination here is an
// existing directory — renaming a file over it fails on every platform — so the
// failure is deterministic without touching permissions.
func TestWriteAtomicFailureKeepsTarget(t *testing.T) {
	dir := t.TempDir()
	// The destination IS an existing directory: renaming a file over a
	// directory fails on every platform (EISDIR on POSIX, access denied on
	// Windows), which makes the publish failure deterministic.
	target := filepath.Join(dir, "manifest.xml")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := WriteAtomic(target, []byte("<file name=\"v\"/>"), 0o644); err == nil {
		t.Fatal("publish over an existing directory succeeded, want a rename failure")
	}

	// The directory must still be there — a failed publish may not remove what
	// it could not replace.
	if fi, err := os.Stat(target); err != nil || !fi.IsDir() {
		t.Fatalf("existing target was destroyed: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "manifest.xml" {
			t.Errorf("leftover after failed publish: %s", e.Name())
		}
	}
}

// TestSplitChunksExtremes pins the overflow-free count and closing bound at the
// int64 edges: the naive ceil `(t+c-1)/c` wraps negative there and the naive
// `from+chunkSize-1` closes past the end.
func TestSplitChunksExtremes(t *testing.T) {
	plans, err := SplitChunks(math.MaxInt64, math.MaxInt64)
	if err != nil {
		t.Fatalf("MaxInt64/MaxInt64: %v", err)
	}
	if len(plans) != 1 || plans[0].From != 0 || plans[0].To != math.MaxInt64-1 {
		t.Fatalf("MaxInt64/MaxInt64 plans = %+v, want one chunk closing at MaxInt64-1", plans)
	}

	plans, err = SplitChunks(math.MaxInt64, math.MaxInt64-1)
	if err != nil {
		t.Fatalf("MaxInt64/(MaxInt64-1): %v", err)
	}
	if len(plans) != 2 ||
		plans[0].To != math.MaxInt64-2 ||
		plans[1].From != math.MaxInt64-1 ||
		plans[1].To != math.MaxInt64-1 {
		t.Fatalf("MaxInt64/(MaxInt64-1) plans = %+v, want the second chunk to be the final byte", plans)
	}

	plans, err = SplitChunks(1<<32, 1<<20)
	if err != nil {
		t.Fatalf("4 GiB/1 MiB: %v", err)
	}
	if len(plans) != 4096 || plans[4095].To != (1<<32)-1 {
		t.Errorf("4 GiB/1 MiB count = %d, want 4096 closing at the last byte", len(plans))
	}
}

// TestParseRejectsTrailingContent pins the full-document contract: the input
// ends with the root element (plus whitespace), so a second root or trailing
// garbage is a syntax error rather than a manifest that parses and then
// misbehaves under Validate.
func TestParseRejectsTrailingContent(t *testing.T) {
	const root = `<file name="a.bin" chunks="1" total_size="4" md5="00000000000000000000000000000000"><chunk id="0" from="0" to="3" method="md5">00000000000000000000000000000000</chunk></file>`

	t.Run("trailing whitespace is accepted", func(t *testing.T) {
		if _, err := Parse(strings.NewReader(root + "\n \t\n")); err != nil {
			t.Errorf("Parse with trailing whitespace: %v", err)
		}
	})

	t.Run("second root is rejected", func(t *testing.T) {
		if _, err := Parse(strings.NewReader(root + root)); err == nil {
			t.Fatal("Parse of two roots: err = nil, want a syntax error")
		}
	})

	t.Run("trailing garbage is rejected", func(t *testing.T) {
		if _, err := Parse(strings.NewReader(root + "<")); err == nil {
			t.Fatal("Parse of trailing garbage: err = nil, want a syntax error")
		}
	})
}
