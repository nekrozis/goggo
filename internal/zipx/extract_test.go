package zipx

import (
	"bytes"
	"compress/flate"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// fixture helpers (hand-built entries: archive/zip is never the oracle)

// buildLocalEntry writes one local file header followed by raw entry data.
// The declared sizes in p may intentionally disagree with data for tests
// that lock the "no truncation to compressed size" semantics.
func buildLocalEntry(p centralEntryParams, data []byte) []byte {
	var b bytes.Buffer
	w32(&b, zipLocalHeaderSignature)
	w16(&b, 20) // version needed to extract
	w16(&b, 0)  // flags
	w16(&b, p.method)
	w16(&b, p.modTime)
	w16(&b, p.modDate)
	w32(&b, p.crc)
	w32(&b, p.compSize)
	w32(&b, p.uncomp)
	w16(&b, uint16(len(p.name)))
	w16(&b, uint16(len(p.extra)))
	b.WriteString(p.name)
	b.Write(p.extra)
	b.Write(data)
	return b.Bytes()
}

// rawDeflate compresses payload as a raw RFC 1951 stream (no zlib wrapper),
// the format flate.NewReader expects.
func rawDeflate(t *testing.T, payload []byte) []byte {
	t.Helper()
	var b bytes.Buffer
	zw, err := flate.NewWriter(&b, flate.DefaultCompression)
	if err != nil {
		t.Fatalf("flate.NewWriter: %v", err)
	}
	if _, err := zw.Write(payload); err != nil {
		t.Fatalf("deflate write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("deflate close: %v", err)
	}
	return b.Bytes()
}

// ---------------------------------------------------------------------------
// extraction tests

func TestExtractStreamDeflated(t *testing.T) {
	payload := []byte("hello goggo raw-deflate payload")
	comp := rawDeflate(t, payload)
	entry := buildLocalEntry(centralEntryParams{
		name: "a.txt", method: MethodDeflated,
		compSize: uint32(len(comp)), uncomp: uint32(len(payload)),
	}, comp)

	var out bytes.Buffer
	if err := ExtractStream(bytes.NewReader(entry), &out); err != nil {
		t.Fatalf("ExtractStream: %v", err)
	}
	if !bytes.Equal(out.Bytes(), payload) {
		t.Errorf("output = %q, want %q", out.Bytes(), payload)
	}
}

func TestExtractStreamStore(t *testing.T) {
	data := []byte("stored verbatim entry data")
	entry := buildLocalEntry(centralEntryParams{
		name: "s.txt", method: MethodStore,
		compSize: uint32(len(data)), uncomp: uint32(len(data)),
	}, data)

	var out bytes.Buffer
	if err := ExtractStream(bytes.NewReader(entry), &out); err != nil {
		t.Fatalf("ExtractStream: %v", err)
	}
	if !bytes.Equal(out.Bytes(), data) {
		t.Errorf("output = %q, want %q", out.Bytes(), data)
	}
}

// TestExtractDeflateIgnoresTrailingBytes locks the stream-to-end semantics:
// extraction stops at the end of the deflate stream and does not consume or
// fail on bytes that follow the compressed data.
func TestExtractDeflateIgnoresTrailingBytes(t *testing.T) {
	payload := []byte("data before trailing junk")
	comp := rawDeflate(t, payload)
	entry := buildLocalEntry(centralEntryParams{
		name: "a.txt", method: MethodDeflated,
		compSize: uint32(len(comp)), uncomp: uint32(len(payload)),
	}, append(comp, []byte("TRAILING-GARBAGE")...))

	var out bytes.Buffer
	if err := ExtractStream(bytes.NewReader(entry), &out); err != nil {
		t.Fatalf("ExtractStream: %v", err)
	}
	if !bytes.Equal(out.Bytes(), payload) {
		t.Errorf("output = %q, want %q (trailing bytes must not leak in)", out.Bytes(), payload)
	}
}

// TestExtractStoreNotTruncatedToCompSize locks the no-truncation rule for
// stored entries: the declared compressed size is smaller than the data
// present, yet everything up to EOF is copied ( copies to EOF).
func TestExtractStoreNotTruncatedToCompSize(t *testing.T) {
	data := []byte("0123456789abcdef")
	entry := buildLocalEntry(centralEntryParams{
		name: "s.txt", method: MethodStore,
		compSize: 4, uncomp: 4, // declared smaller than the 16 real bytes
	}, data)

	var out bytes.Buffer
	if err := ExtractStream(bytes.NewReader(entry), &out); err != nil {
		t.Fatalf("ExtractStream: %v", err)
	}
	if !bytes.Equal(out.Bytes(), data) {
		t.Errorf("output = %q, want all %d bytes (no comp_size truncation)", out.Bytes(), len(data))
	}
}

// TestExtractStoreShortInputNoError: a stored entry whose input ends early is
// just an early EOF for io.Copy, NOT an error — there is no decompressor on this
// path and sizes are never validated.
func TestExtractStoreShortInputNoError(t *testing.T) {
	entry := buildLocalEntry(centralEntryParams{
		name: "s.txt", method: MethodStore,
		compSize: 100, uncomp: 100, // declared, but no data bytes follow
	}, nil)

	var out bytes.Buffer
	if err := ExtractStream(bytes.NewReader(entry), &out); err != nil {
		t.Fatalf("ExtractStream: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("output length = %d, want 0", out.Len())
	}
}

func TestExtractTruncatedDeflateErrors(t *testing.T) {
	payload := []byte("this payload is long enough to survive a mid-stream cut")
	comp := rawDeflate(t, payload)
	entry := buildLocalEntry(centralEntryParams{
		name: "a.txt", method: MethodDeflated,
		compSize: uint32(len(comp)), uncomp: uint32(len(payload)),
	}, comp[:len(comp)/2]) // cut the deflate stream in half

	var out bytes.Buffer
	if err := ExtractStream(bytes.NewReader(entry), &out); err == nil {
		t.Fatal("ExtractStream: want error for truncated deflate stream")
	}
}

func TestExtractDamagedDeflateEndErrors(t *testing.T) {
	payload := []byte("payload whose stream end we destroy")
	comp := rawDeflate(t, payload)
	entry := buildLocalEntry(centralEntryParams{
		name: "a.txt", method: MethodDeflated,
		compSize: uint32(len(comp)), uncomp: uint32(len(payload)),
	}, comp[:len(comp)-2]) // remove the final block terminator bytes

	var out bytes.Buffer
	if err := ExtractStream(bytes.NewReader(entry), &out); err == nil {
		t.Fatal("ExtractStream: want error for damaged deflate stream end")
	}
}

func TestExtractUnsupportedMethod(t *testing.T) {
	entry := buildLocalEntry(centralEntryParams{
		name: "b.txt", method: 12, // bzip2: not store, not deflate
	}, []byte("ignored"))
	var out bytes.Buffer
	err := ExtractStream(bytes.NewReader(entry), &out)
	if !errors.Is(err, ErrUnsupportedMethod) {
		t.Fatalf("err = %v, want ErrUnsupportedMethod", err)
	}
}

// TestExtractCentralRecordFollowedByData documents that ParseCDEntry accepts
// both header layouts, so ExtractStream also works when the stream begins at
// a central-directory record whose data directly follows it.
func TestExtractCentralRecordFollowedByData(t *testing.T) {
	data := []byte("central record then data")
	rec := buildCentralEntry(centralEntryParams{
		name: "c.txt", method: MethodStore,
		compSize: uint32(len(data)), uncomp: uint32(len(data)),
	})
	input := append(rec, data...)

	var out bytes.Buffer
	if err := ExtractStream(bytes.NewReader(input), &out); err != nil {
		t.Fatalf("ExtractStream: %v", err)
	}
	if !bytes.Equal(out.Bytes(), data) {
		t.Errorf("output = %q, want %q", out.Bytes(), data)
	}
}

func TestExtractFileSetsTimestampAndContent(t *testing.T) {
	payload := []byte("extracted through ExtractFile")
	comp := rawDeflate(t, payload)
	wantTime := time.Date(2020, 4, 15, 12, 30, 0, 0, time.Local).Unix()
	md := uint16((2020-1980)<<9 | 4<<5 | 15) // 2020-04-15
	mt := uint16(12<<11 | 30<<5)             // 12:30:00 (sec 0)
	entry := buildLocalEntry(centralEntryParams{
		name: "a.bin", method: MethodDeflated, modDate: md, modTime: mt,
		compSize: uint32(len(comp)), uncomp: uint32(len(payload)),
	}, comp)

	dir := t.TempDir()
	inPath := filepath.Join(dir, "in.bin")
	outPath := filepath.Join(dir, "out.bin")
	if err := os.WriteFile(inPath, entry, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ExtractFile(inPath, outPath); err != nil {
		t.Fatalf("ExtractFile: %v", err)
	}
	got, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("content = %q, want %q", got, payload)
	}
	fi, err := os.Stat(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if fi.ModTime().Unix() != wantTime {
		t.Errorf("mtime = %d, want %d", fi.ModTime().Unix(), wantTime)
	}
}

// TestExtractFileZeroTimestampSkipsChtimes: an invalid DOS date yields
// Timestamp 0, so extraction must not attempt to set the mtime.
func TestExtractFileZeroTimestampSkipsChtimes(t *testing.T) {
	data := []byte("no timestamp entry")
	entry := buildLocalEntry(centralEntryParams{
		name: "z.bin", method: MethodStore, // modDate/modTime 0 -> timestamp 0
		compSize: uint32(len(data)), uncomp: uint32(len(data)),
	}, data)

	dir := t.TempDir()
	inPath := filepath.Join(dir, "in.bin")
	outPath := filepath.Join(dir, "out.bin")
	if err := os.WriteFile(inPath, entry, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ExtractFile(inPath, outPath); err != nil {
		t.Fatalf("ExtractFile: %v", err)
	}
	got, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("content = %q, want %q", got, data)
	}
}
