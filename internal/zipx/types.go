package zipx

// EOCD mirrors struct zipEOCD (ziputil.h:24-36). Field order follows the
// memory-minimisation rule (string block first, then integers by size).
type EOCD struct {
	Comment        string
	Signature      uint32
	Disk           uint16
	CDStartDisk    uint16
	CDRecords      uint16
	TotalCDRecords uint16
	CDSize         uint32
	CDStartOffset  uint32
	CommentLength  uint16
}

// Zip64EOCD mirrors struct zip64EOCD (ziputil.h:38-52). The zip64 locator
// record is not parsed (the original code does not read it either).
type Zip64EOCD struct {
	Comment             string
	DirectoryRecordSize uint64
	VersionMadeBy       uint16
	VersionNeeded       uint16
	Signature           uint32
	Disk                uint32
	CDStartDisk         uint32
	CDTotalDisk         uint64
	CDTotal             uint64
	CDSize              uint64
	CDOffset            uint64
}

// CDEntry mirrors struct zipCDEntry (ziputil.h:54-79). IsLocal is true when
// the entry was read from a local file header; central-directory-only
// fields are then zero (ziputil.cpp:258-314).
type CDEntry struct {
	Extra             []byte
	FileName          string
	Comment           string
	Timestamp         int64
	CompressedSize    uint64
	UncompressedSize  uint64
	DiskOffset        uint64
	Signature         uint32
	VersionMadeBy     uint16
	VersionNeeded     uint16
	Flags             uint16
	CompressionMethod uint16
	ModTime           uint16
	ModDate           uint16
	CRC32             uint32
	FileNameLength    uint16
	ExtraLength       uint16
	CommentLength     uint16
	DiskNumber        uint32
	InternalAttr      uint16
	ExternalAttr      uint32
	IsLocal           bool
}
