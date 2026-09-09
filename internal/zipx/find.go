package zipx

import "encoding/binary"

// findSignature scans data backwards from the end looking for a 4-byte
// signature (ziputil.cpp:133-152). It returns the byte offset of the
// signature, or ok=false when absent. The C++ -1 sentinel is intentionally
// not used in the package API.
func findSignature(data []byte, sig uint32) (int64, bool) {
	for i := 4; i <= len(data); i++ {
		pos := len(data) - i
		if binary.LittleEndian.Uint32(data[pos:pos+4]) == sig {
			return int64(pos), true
		}
	}
	return 0, false
}

// findEOCD locates the End Of Central Directory signature.
func findEOCD(data []byte) (int64, bool) {
	return findSignature(data, zipEOCDHeaderSignature)
}

// findZip64EOCD locates the ZIP64 End Of Central Directory signature.
func findZip64EOCD(data []byte) (int64, bool) {
	return findSignature(data, zip64EOCDHeaderSignature)
}
