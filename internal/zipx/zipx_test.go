package zipx

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"io/fs"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// fixture helpers

// writeU16/u32/u64 append little-endian integers.
func w16(b *bytes.Buffer, v uint16) { _ = binary.Write(b, binary.LittleEndian, v) }
func w32(b *bytes.Buffer, v uint32) { _ = binary.Write(b, binary.LittleEndian, v) }
func w64(b *bytes.Buffer, v uint64) { _ = binary.Write(b, binary.LittleEndian, v) }

// makeZipWithComment builds a normal zip using archive/zip. It is a FIXTURE
// GENERATOR only, never an implementation oracle.
func makeZipWithComment(t *testing.T, entries []string, comment string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range entries {
		f, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zw.Create: %v", err)
		}
		if _, err := f.Write([]byte("content-of-" + name)); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	if err := zw.SetComment(comment); err != nil {
		t.Fatalf("SetComment: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return buf.Bytes()
}

// centralEntryParams describes a crafted central directory entry.
type centralEntryParams struct {
	name         string
	comment      string
	method       uint16
	crc          uint32
	compSize     uint32 // sentinel allowed for zip64 tests
	uncomp       uint32
	diskOffset   uint32
	modDate      uint16
	modTime      uint16
	extra        []byte
	externalAttr uint32
}

// buildCentralEntry writes one central directory entry header.
func buildCentralEntry(p centralEntryParams) []byte {
	var b bytes.Buffer
	w32(&b, zipCDHeaderSignature)
	w16(&b, 20) // version made by
	w16(&b, 20) // version needed
	w16(&b, 0)  // flags
	w16(&b, p.method)
	w16(&b, p.modTime)
	w16(&b, p.modDate)
	w32(&b, p.crc)
	w32(&b, p.compSize)
	w32(&b, p.uncomp)
	w16(&b, uint16(len(p.name)))
	w16(&b, uint16(len(p.extra)))
	w16(&b, uint16(len(p.comment)))
	w16(&b, 0) // disk start
	w16(&b, 0) // internal attrs
	w32(&b, p.externalAttr)
	w32(&b, p.diskOffset)
	b.WriteString(p.name)
	b.Write(p.extra)
	b.WriteString(p.comment)
	return b.Bytes()
}

// eocdBytes builds a minimal EOCD record.
func eocdBytes(cdSize, cdOffset, total uint16, comment string) []byte {
	var b bytes.Buffer
	w32(&b, zipEOCDHeaderSignature)
	w16(&b, 0) // disk
	w16(&b, 0) // cd start disk
	w16(&b, total)
	w16(&b, total)
	w32(&b, uint32(cdSize))
	w32(&b, uint32(cdOffset))
	w16(&b, uint16(len(comment)))
	b.WriteString(comment)
	return b.Bytes()
}

// ---------------------------------------------------------------------------
// tests

func TestFindEOCDPositiveAndNegative(t *testing.T) {
	data := makeZipWithComment(t, []string{"a.txt"}, "c")
	if off, ok := findEOCD(data); !ok || off < 0 {
		t.Fatalf("findEOCD = %d,%v", off, ok)
	}
	if off, ok := findEOCD([]byte("no zip here")); ok || off != 0 {
		t.Errorf("findEOCD on garbage = %d,%v, want 0,false", off, ok)
	}
	if off, ok := findZip64EOCD(makeZipWithComment(t, []string{"a"}, "")); ok || off != 0 {
		t.Errorf("findZip64EOCD on normal zip = %d,%v", off, ok)
	}
}

func TestParseEOCDRoundTrip(t *testing.T) {
	data := makeZipWithComment(t, []string{"a.txt", "b.txt"}, "hello-comment")
	eocd, err := ParseEOCD(data)
	if err != nil {
		t.Fatalf("ParseEOCD: %v", err)
	}
	if eocd.TotalCDRecords != 2 {
		t.Errorf("TotalCDRecords = %d, want 2", eocd.TotalCDRecords)
	}
	if eocd.Comment != "hello-comment" {
		t.Errorf("comment = %q", eocd.Comment)
	}
	if _, err := ParseEOCD([]byte("junk")); err == nil {
		t.Error("ParseEOCD on junk should fail")
	}
}

func TestParseCentralAndLocalEntries(t *testing.T) {
	data := makeZipWithComment(t, []string{"a.txt"}, "")
	eocd, err := ParseEOCD(data)
	if err != nil {
		t.Fatal(err)
	}
	cdStart := int64(eocd.CDStartOffset)
	entry, err := ParseCDEntry(bytes.NewReader(data[cdStart:]))
	if err != nil {
		t.Fatalf("central ParseCDEntry: %v", err)
	}
	if entry.IsLocal {
		t.Error("central entry must not be local")
	}
	if entry.FileName != "a.txt" {
		t.Errorf("filename = %q", entry.FileName)
	}
	if entry.CompressedSize == 0 {
		t.Error("compressed size zero")
	}
	// Parse the local header at the recorded offset.
	local, err := ParseCDEntry(bytes.NewReader(data[entry.DiskOffset:]))
	if err != nil {
		t.Fatalf("local ParseCDEntry: %v", err)
	}
	if !local.IsLocal {
		t.Error("local entry not marked local")
	}
	if local.FileName != "a.txt" {
		t.Errorf("local filename = %q", local.FileName)
	}
}

func TestZip64ExtraPerFieldSentinels(t *testing.T) {
	// Both uncompressed and compressed sizes carry the sentinel; the extra
	// must contain two uint64 values (per-field consumption, in order).
	var extra bytes.Buffer
	w16(&extra, zipExtensionZip64)
	w16(&extra, 16)
	w64(&extra, 9876543210) // uncompressed
	w64(&extra, 1234567890) // compressed
	entry := buildCentralEntry(centralEntryParams{
		name: "big.bin", method: 0, crc: 1,
		compSize: zip64Uint32Max, uncomp: zip64Uint32Max,
		extra: extra.Bytes(),
	})
	cd, err := ParseCDEntry(bytes.NewReader(entry))
	if err != nil {
		t.Fatalf("ParseCDEntry: %v", err)
	}
	if cd.UncompressedSize != 9876543210 {
		t.Errorf("uncompressed = %d", cd.UncompressedSize)
	}
	if cd.CompressedSize != 1234567890 {
		t.Errorf("compressed = %d", cd.CompressedSize)
	}
}

func TestZip64ExtraTruncatedErrors(t *testing.T) {
	var extra bytes.Buffer
	w16(&extra, zipExtensionZip64)
	w16(&extra, 8) // claims 8 bytes but only one value for two sentinels
	w64(&extra, 100)
	entry := buildCentralEntry(centralEntryParams{
		name: "x", method: 0, uncomp: zip64Uint32Max, compSize: zip64Uint32Max,
		extra: extra.Bytes(),
	})
	if _, err := ParseCDEntry(bytes.NewReader(entry)); err == nil {
		t.Error("truncated zip64 extra must error")
	}
}

func TestExtendedTimestampOverridesDOS(t *testing.T) {
	dosDate := uint16((2020-1980)<<9 | 4<<5 | 15) // 2020-04-15 (DOS month field is 1-12)
	dosTime := uint16(12<<11 | 30<<5 | 0)         // 12:30:00
	wantDOS := time.Date(2020, 4, 15, 12, 30, 0, 0, time.Local).Unix()

	// Extra field wrapper: header id 0x5455 + size + payload.
	tsExtra := func(content []byte) []byte {
		var b bytes.Buffer
		w16(&b, zipExtendedTimestamp)
		w16(&b, uint16(len(content)))
		b.Write(content)
		return b.Bytes()
	}

	// Case 1: mtime flag set -> extended value wins.
	extTS := uint64(0x78abcdef) // LE bytes: ef cd ab 78
	entry := buildCentralEntry(centralEntryParams{
		name: "t", method: 0, modDate: dosDate, modTime: dosTime,
		extra: tsExtra([]byte{0x01, 0xef, 0xcd, 0xab, 0x78}),
	})
	cd, err := ParseCDEntry(bytes.NewReader(entry))
	if err != nil {
		t.Fatal(err)
	}
	if cd.Timestamp != int64(extTS) {
		t.Errorf("extended timestamp = %d, want %d", cd.Timestamp, int64(extTS))
	}

	// Case 2: extra present but mtime flag unset -> DOS timestamp kept.
	entry = buildCentralEntry(centralEntryParams{
		name: "t2", method: 0, modDate: dosDate, modTime: dosTime,
		extra: tsExtra([]byte{0x00, 0xef, 0xcd, 0xab, 0x78}),
	})
	cd, err = ParseCDEntry(bytes.NewReader(entry))
	if err != nil {
		t.Fatal(err)
	}
	if cd.Timestamp != wantDOS {
		t.Errorf("no-mtime-flag timestamp = %d, want DOS %d", cd.Timestamp, wantDOS)
	}
}

func TestDOSDateTimeLocal(t *testing.T) {
	// DOS date/time -> local wall clock (time.Local is used on purpose).
	date := uint16((2020-1980)<<9 | 1<<5 | 2)
	clock := uint16(3<<11 | 4<<5 | 3) // seconds field 3 => 6s
	tm, ok := dosDateTimeToTime(date, clock)
	if !ok {
		t.Fatal("expected valid")
	}
	want := time.Date(2020, 1, 2, 3, 4, 6, 0, time.Local)
	if !tm.Equal(want) {
		t.Errorf("dosDateTimeToTime = %v, want %v", tm, want)
	}
	// Invalid hour (31) rejected.
	if _, ok := dosDateTimeToTime(date, 31<<11); ok {
		t.Error("hour 31 must be invalid")
	}
	// Invalid seconds (31*2 = 62) rejected.
	if _, ok := dosDateTimeToTime(date, 31); ok {
		t.Error("seconds 62 must be invalid")
	}
	// Day 0 invalid.
	if _, ok := dosDateTimeToTime(date&^uint16(0x1f), clock); ok {
		t.Error("day 0 must be invalid")
	}
}

func TestUnixModeAndSymlink(t *testing.T) {
	if got := fileModeFromUnixMode(0o755); got != fs.FileMode(0o755) {
		t.Errorf("mode = %v", got)
	}
	if got := fileModeFromUnixMode(0o644); got != fs.FileMode(0o644) {
		t.Errorf("mode = %v", got)
	}
	// Mode bits only: setuid/setgid bits are not expressible and dropped.
	if got := fileModeFromUnixMode(0o4755); got != fs.FileMode(0o755) {
		t.Errorf("mode with setuid = %v, want 0755 (not lossless)", got)
	}
	if !isSymlink(0o120777) {
		t.Error("S_IFLNK must be detected")
	}
	if isSymlink(0o100644) {
		t.Error("regular file must not be a symlink")
	}

	// unixMode extracts the high 16 bits of external attributes.
	e := centralEntryParams{name: "f", method: 0, externalAttr: uint32(0o100755) << 16}
	entry := buildCentralEntry(e)
	cd, err := ParseCDEntry(bytes.NewReader(entry))
	if err != nil {
		t.Fatal(err)
	}
	if got := unixMode(cd); got != 0o100755 {
		t.Errorf("unixMode = %o", got)
	}
	if fileModeFromUnixMode(unixMode(cd)) != fs.FileMode(0o755) {
		t.Error("perm mapping mismatch")
	}
}

// TestMalformedInputNeverPanics is a panic guard, not a behaviour test: its
// loops discard every result on purpose. The one behavioural claim is at the
// end (the valid archive still parses). Keep it in that role - it exists to
// catch an index out of range on hostile input, which the table-driven tests
// above cannot enumerate.
func TestMalformedInputNeverPanics(t *testing.T) {
	valid := makeZipWithComment(t, []string{"a.txt", "b.txt"}, "c")

	// Truncations of a real archive.
	for cut := 0; cut <= len(valid); cut++ {
		part := valid[:cut]
		_, _ = ParseEOCD(part)
		if off, ok := findEOCD(part); ok {
			_, _ = readEOCD(bytes.NewReader(part[off:]))
		}
	}
	// Random/garbage inputs.
	seed := uint32(0x1234)
	for i := 0; i < 200; i++ {
		seed = seed*1664525 + 1013904223
		n := int(seed % 40)
		buf := make([]byte, n)
		for j := range buf {
			seed = seed*1664525 + 1013904223
			buf[j] = byte(seed)
		}
		_, _ = ParseEOCD(buf)
		_, _ = ParseCDEntry(bytes.NewReader(buf))
		_, _ = readZip64EOCD(bytes.NewReader(buf))
	}
	// Explicit truncated entry: filename length overruns data.
	var b bytes.Buffer
	w32(&b, zipCDHeaderSignature)
	w16(&b, 20) // version made by
	w16(&b, 20) // version needed
	w16(&b, 0)
	w16(&b, 0)
	w16(&b, 0)
	w16(&b, 0)
	w32(&b, 0)
	w32(&b, 0)
	w32(&b, 0)
	w16(&b, 65535) // filename length huge
	w16(&b, 0)     // extra
	w16(&b, 0)     // comment
	w16(&b, 0)
	w16(&b, 0)
	w32(&b, 0)
	w32(&b, 0)
	if _, err := ParseCDEntry(bytes.NewReader(b.Bytes())); err == nil {
		t.Error("truncated filename must error")
	}
}

func TestParseEOCDReadsComment(t *testing.T) {
	// EOCD located at the end of appended comment is found by backward scan.
	data := makeZipWithComment(t, []string{"x"}, "tail")
	part := append(data, []byte("padding-beyond-eocd")...)
	eocd, err := ParseEOCD(part)
	if err != nil {
		t.Fatalf("ParseEOCD: %v", err)
	}
	if eocd.Comment != "tail" {
		t.Errorf("comment = %q", eocd.Comment)
	}
}
