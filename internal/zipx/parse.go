package zipx

// ZIP signatures and extra-field header ids.
const (
	zipLocalHeaderSignature  uint32 = 0x04034b50
	zipCDHeaderSignature     uint32 = 0x02014b50
	zipEOCDHeaderSignature   uint32 = 0x06054b50
	zip64EOCDHeaderSignature uint32 = 0x06064b50

	zipExtensionZip64    uint16 = 0x0001
	zipExtendedTimestamp uint16 = 0x5455
	zipInfoZipUnixNew    uint16 = 0x7875
)

// ZIP64 sentinel values that mark "the real value lives in the 0x0001 extra".
const (
	zip64Uint16Max = uint16(0xffff)
	zip64Uint32Max = uint32(0xffffffff)
)
