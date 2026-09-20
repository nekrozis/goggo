package zipx

import (
	"compress/flate"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

// ZIP compression methods supported by extraction.
const (
	MethodStore    uint16 = 0
	MethodDeflated uint16 = 8
)

// ErrUnsupportedMethod reports an entry whose compression method is neither
// store nor deflate.
var ErrUnsupportedMethod = errors.New("zipx: unsupported compression method")

// readEntry parses one entry header from r and validates its method. On
// success r is positioned at the start of the entry data (after the name and
// extra field, and after the comment for central-directory records).
func readEntry(r io.Reader) (CDEntry, error) {
	cd, err := ParseCDEntry(r)
	if err != nil {
		return cd, err
	}
	if cd.CompressionMethod != MethodStore && cd.CompressionMethod != MethodDeflated {
		return cd, fmt.Errorf("zipx: method %d: %w", cd.CompressionMethod, ErrUnsupportedMethod)
	}
	return cd, nil
}

// copyEntry writes the entry data at r's current position to w. Deflated data
// is inflated as a raw RFC 1951 stream (no zlib header, no checksum); stored
// data is copied verbatim to EOF. Neither path truncates to the declared
// compressed size.
func copyEntry(r io.Reader, w io.Writer, method uint16) error {
	if method == MethodDeflated {
		zr := flate.NewReader(r)
		defer zr.Close()
		if _, err := io.Copy(w, zr); err != nil {
			return fmt.Errorf("zipx: inflate: %w", err)
		}
		return nil
	}
	// MethodStore: no decompressor, so a short input is just an early EOF, not
	// an error.
	if _, err := io.Copy(w, r); err != nil {
		return err
	}
	return nil
}

// ExtractStream consumes one entry from r and writes its data to w.
//
// Contract: r must begin at a local-file header (ParseCDEntry also accepts a
// central-directory record, in which case data is read from directly after
// it). The header is consumed first; the entry data is then copied to EOF for
// stored entries or until the raw deflate stream ends. The declared compressed
// size is never used to truncate, and no CRC or size validation is performed.
func ExtractStream(r io.Reader, w io.Writer) error {
	cd, err := readEntry(r)
	if err != nil {
		return err
	}
	return copyEntry(r, w, cd.CompressionMethod)
}

// ExtractFile extracts the first entry of inPath into outPath. The input file
// is read from offset 0 and must therefore contain the entry as its first
// record. On success, when the entry timestamp is positive, the output file's
// modification time is set to it.
func ExtractFile(inPath, outPath string) error {
	in, err := os.Open(inPath)
	if err != nil {
		return fmt.Errorf("zipx: open input: %w", err)
	}
	defer in.Close()

	cd, err := readEntry(in)
	if err != nil {
		return err
	}

	out, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("zipx: create output: %w", err)
	}
	if err := copyEntry(in, out, cd.CompressionMethod); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}

	if cd.Timestamp > 0 {
		ts := time.Unix(cd.Timestamp, 0)
		if err := os.Chtimes(outPath, ts, ts); err != nil {
			return fmt.Errorf("zipx: set timestamp: %w", err)
		}
	}
	return nil
}
