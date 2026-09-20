package zipx

import (
	"bytes"
	"fmt"
	"io"
)

// readEOCD parses an EOCD structure at the current stream position.
func readEOCD(r io.Reader) (EOCD, error) {
	var e EOCD
	var err error
	if e.Signature, err = readU32(r); err != nil {
		return e, err
	}
	if e.Signature != zipEOCDHeaderSignature {
		return e, fmt.Errorf("zipx: not an EOCD record (0x%08x)", e.Signature)
	}
	if e.Disk, err = readU16(r); err != nil {
		return e, err
	}
	if e.CDStartDisk, err = readU16(r); err != nil {
		return e, err
	}
	if e.CDRecords, err = readU16(r); err != nil {
		return e, err
	}
	if e.TotalCDRecords, err = readU16(r); err != nil {
		return e, err
	}
	if e.CDSize, err = readU32(r); err != nil {
		return e, err
	}
	if e.CDStartOffset, err = readU32(r); err != nil {
		return e, err
	}
	if e.CommentLength, err = readU16(r); err != nil {
		return e, err
	}
	if e.CommentLength > 0 {
		buf, err := readBytes(r, int(e.CommentLength))
		if err != nil {
			return e, err
		}
		e.Comment = string(buf)
	}
	return e, nil
}

// readZip64EOCD parses a ZIP64 EOCD structure. The
// trailing extensible data sector is intentionally not read.
func readZip64EOCD(r io.Reader) (Zip64EOCD, error) {
	var e Zip64EOCD
	var err error
	if e.Signature, err = readU32(r); err != nil {
		return e, err
	}
	if e.Signature != zip64EOCDHeaderSignature {
		return e, fmt.Errorf("zipx: not a ZIP64 EOCD record (0x%08x)", e.Signature)
	}
	if e.DirectoryRecordSize, err = readU64(r); err != nil {
		return e, err
	}
	if e.VersionMadeBy, err = readU16(r); err != nil {
		return e, err
	}
	if e.VersionNeeded, err = readU16(r); err != nil {
		return e, err
	}
	if e.Disk, err = readU32(r); err != nil {
		return e, err
	}
	if e.CDStartDisk, err = readU32(r); err != nil {
		return e, err
	}
	if e.CDTotalDisk, err = readU64(r); err != nil {
		return e, err
	}
	if e.CDTotal, err = readU64(r); err != nil {
		return e, err
	}
	if e.CDSize, err = readU64(r); err != nil {
		return e, err
	}
	if e.CDOffset, err = readU64(r); err != nil {
		return e, err
	}
	return e, nil
}

// ParseEOCD locates the EOCD record in data and parses it.
func ParseEOCD(data []byte) (EOCD, error) {
	offset, ok := findEOCD(data)
	if !ok {
		return EOCD{}, fmt.Errorf("zipx: EOCD record not found")
	}
	return readEOCD(bytes.NewReader(data[offset:]))
}

// ParseZip64EOCD locates the ZIP64 EOCD record in data and parses it.
func ParseZip64EOCD(data []byte) (Zip64EOCD, error) {
	offset, ok := findZip64EOCD(data)
	if !ok {
		return Zip64EOCD{}, fmt.Errorf("zipx: zip64 EOCD record not found")
	}
	return readZip64EOCD(bytes.NewReader(data[offset:]))
}
