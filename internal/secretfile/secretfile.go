// Package secretfile frames a small private file: a magic, a version, a length,
// an obfuscated payload and a checksum.
//
// The framing knows nothing about what a payload MEANS. What a payload must
// contain is the caller's contract — a token tree for one caller, cookie records
// for another — so a payload that parses at this layer and is wrong at the
// caller's layer is reported by the caller. That boundary is why the package has
// no notion of tokens or cookies: a shared layer that understood either would
// grow a second meaning for every caller it gained.
//
// The obfuscation is NOT encryption. The key is compiled into the program, so
// anyone holding it can reverse the payload. Its only effect is that the file is
// no longer text: a plain grep, a text index or an accidental upload does not
// pick up the fields. It provides no cryptographic confidentiality and is not
// meant to resist malware, EDR or anyone analysing the machine.
package secretfile

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
)

// CRCLen is the size of the trailing checksum. The checksum covers truncation,
// random corruption and a partial write; it is an integrity check, not a
// security measure, since rewriting the payload lets an attacker rewrite it too.
const CRCLen = 4

// The five ways the framing itself can fail. A payload that fails to parse at
// the caller's layer is the caller's own error, not one of these.
var (
	ErrTruncated = errors.New("file is shorter than its header")
	ErrMagic     = errors.New("file magic does not match")
	ErrVersion   = errors.New("file version is not supported")
	ErrLength    = errors.New("declared length does not match the file")
	ErrCRC       = errors.New("file checksum does not match")
)

// Obfuscator transforms a payload for storage and back. It must be its own
// inverse, because the same value is applied in both directions.
type Obfuscator func([]byte) []byte

// XOR returns an Obfuscator that XORs the payload against a repeating key. The
// key is obfuscation material, not a secret: it lives in the caller's source.
//
// Each caller passes its own key, so two file kinds sharing this framing do not
// share a payload encoding either.
func XOR(key string) Obfuscator {
	k := []byte(key)
	return func(data []byte) []byte {
		out := make([]byte, len(data))
		for i := range data {
			out[i] = data[i] ^ k[i%len(k)]
		}
		return out
	}
}

// headerLen is magic + version + length. It is derived from the caller's magic
// rather than fixed, so the framing does not constrain how a caller names its
// file kind.
func headerLen(magic string) int {
	return len(magic) + 1 + 4
}

// Encode frames payload: magic | version | length | obfuscated payload | crc32.
func Encode(magic string, version byte, obf Obfuscator, payload []byte) []byte {
	stored := obf(payload)

	out := make([]byte, 0, headerLen(magic)+len(stored)+CRCLen)
	out = append(out, magic...)
	out = append(out, version)
	out = binary.BigEndian.AppendUint32(out, uint32(len(stored)))
	out = append(out, stored...)
	return binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(stored))
}

// Decode reverses Encode and returns the payload.
//
// The checks run in the order their failures can be told apart:
//
//	header completeness -> magic -> version -> declared length -> checksum
//
// The length check comes before the checksum on purpose: a truncated file is a
// length failure, and reporting it as a checksum failure would lose the reason.
func Decode(magic string, version byte, obf Obfuscator, data []byte) ([]byte, error) {
	hl := headerLen(magic)
	if len(data) < hl {
		return nil, ErrTruncated
	}
	if string(data[:len(magic)]) != magic {
		return nil, ErrMagic
	}
	if data[len(magic)] != version {
		return nil, ErrVersion
	}
	length := int(binary.BigEndian.Uint32(data[len(magic)+1:]))
	if len(data) != hl+length+CRCLen {
		return nil, ErrLength
	}

	stored := data[hl : hl+length]
	if got, want := binary.BigEndian.Uint32(data[hl+length:]), crc32.ChecksumIEEE(stored); got != want {
		return nil, ErrCRC
	}
	return obf(stored), nil
}
