package zipx

import (
	"bytes"
	"errors"
	"fmt"
	"io"
)

// ParseCDEntry reads one entry from a stream positioned at its header and
// returns the parsed entry. It handles both central-directory and local-file
// header layouts and the 0x0001 ZIP64, 0x5455 extended-timestamp and 0x7875
// unix extra fields. Errors are returned instead of panicking on truncated
// input.
func ParseCDEntry(r io.Reader) (CDEntry, error) {
	var cd CDEntry
	header, err := readU32(r)
	if err != nil {
		return cd, err
	}
	cd.Signature = header
	cd.IsLocal = header == zipLocalHeaderSignature
	if header != zipLocalHeaderSignature && header != zipCDHeaderSignature {
		return cd, fmt.Errorf("zipx: unexpected entry signature 0x%08x", header)
	}

	if !cd.IsLocal {
		if cd.VersionMadeBy, err = readU16(r); err != nil {
			return cd, err
		}
	}
	if cd.VersionNeeded, err = readU16(r); err != nil {
		return cd, err
	}
	if cd.Flags, err = readU16(r); err != nil {
		return cd, err
	}
	if cd.CompressionMethod, err = readU16(r); err != nil {
		return cd, err
	}
	if cd.ModTime, err = readU16(r); err != nil {
		return cd, err
	}
	if cd.ModDate, err = readU16(r); err != nil {
		return cd, err
	}
	if cd.CRC32, err = readU32(r); err != nil {
		return cd, err
	}
	comp32, err := readU32(r)
	if err != nil {
		return cd, err
	}
	uncomp32, err := readU32(r)
	if err != nil {
		return cd, err
	}
	cd.CompressedSize = uint64(comp32)
	cd.UncompressedSize = uint64(uncomp32)
	if cd.FileNameLength, err = readU16(r); err != nil {
		return cd, err
	}
	if cd.ExtraLength, err = readU16(r); err != nil {
		return cd, err
	}
	if !cd.IsLocal {
		if cd.CommentLength, err = readU16(r); err != nil {
			return cd, err
		}
		disk16, err := readU16(r)
		if err != nil {
			return cd, err
		}
		cd.DiskNumber = uint32(disk16)
		if cd.InternalAttr, err = readU16(r); err != nil {
			return cd, err
		}
		if cd.ExternalAttr, err = readU32(r); err != nil {
			return cd, err
		}
		offset32, err := readU32(r)
		if err != nil {
			return cd, err
		}
		cd.DiskOffset = uint64(offset32)
	}

	name, err := readBytes(r, int(cd.FileNameLength))
	if err != nil {
		return cd, err
	}
	cd.FileName = string(name)

	extra, err := readBytes(r, int(cd.ExtraLength))
	if err != nil {
		return cd, err
	}
	cd.Extra = extra

	// Default timestamp from the DOS date/time pair; 0x5455 may override.
	if t, ok := dosDateTimeToTime(cd.ModDate, cd.ModTime); ok {
		cd.Timestamp = t.Unix()
	} else {
		cd.Timestamp = 0
	}

	if err := parseExtraFields(&cd); err != nil {
		return cd, err
	}

	if !cd.IsLocal {
		comment, err := readBytes(r, int(cd.CommentLength))
		if err != nil {
			return cd, err
		}
		cd.Comment = string(comment)
	}
	return cd, nil
}

// parseExtraFields walks the raw extra field data.
func parseExtraFields(cd *CDEntry) error {
	if len(cd.Extra) == 0 {
		return nil
	}
	br := bytes.NewReader(cd.Extra)
	for br.Len() > 0 {
		headerID, err := readU16(br)
		if err != nil {
			return err
		}
		size, err := readU16(br)
		if err != nil {
			return err
		}
		data := make([]byte, int(size))
		if _, err := io.ReadFull(br, data); err != nil {
			return err
		}
		switch headerID {
		case zipExtensionZip64:
			if err := parseZip64Extra(cd, data); err != nil {
				return err
			}
		case zipExtendedTimestamp:
			parseExtendedTimestamp(cd, data)
		case zipInfoZipUnixNew:
			// Version-gated uid/gid data is skipped.
		default:
			// Unknown extra field: skipped.
		}
	}
	return nil
}

// parseZip64Extra consumes values only for fields whose 32-bit counterpart
// holds the sentinel, in spec order.
func parseZip64Extra(cd *CDEntry, data []byte) error {
	br := bytes.NewReader(data)
	readIf := func() (uint64, error) {
		if br.Len() < 8 {
			return 0, errors.New("zipx: truncated zip64 extra field")
		}
		return readU64(br)
	}
	if cd.UncompressedSize == uint64(zip64Uint32Max) {
		v, err := readIf()
		if err != nil {
			return err
		}
		cd.UncompressedSize = v
	}
	if cd.CompressedSize == uint64(zip64Uint32Max) {
		v, err := readIf()
		if err != nil {
			return err
		}
		cd.CompressedSize = v
	}
	if cd.DiskOffset == uint64(zip64Uint32Max) {
		v, err := readIf()
		if err != nil {
			return err
		}
		cd.DiskOffset = v
	}
	if !cd.IsLocal && cd.DiskNumber == uint32(zip64Uint16Max) {
		v, err := readIf()
		if err != nil {
			return err
		}
		cd.DiskNumber = uint32(v)
	}
	return nil
}

// parseExtendedTimestamp applies the 0x5455 modification time only when its
// info flag bit 0 is set. Access and creation times are read and ignored. When
// the flag is absent the DOS timestamp computed earlier is kept.
func parseExtendedTimestamp(cd *CDEntry, data []byte) {
	if len(data) < 1 {
		return
	}
	if data[0]&0x1 == 0 {
		return
	}
	if len(data) < 5 {
		return
	}
	mtime := uint64(data[1]) | uint64(data[2])<<8 | uint64(data[3])<<16 | uint64(data[4])<<24
	cd.Timestamp = int64(mtime)
}
