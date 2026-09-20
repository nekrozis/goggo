package secretfile

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"testing"
)

// The framing is a caller's choice of magic and version, so the tests use their
// own: nothing here may depend on the token store's or the cookie file's.
const (
	testMagic   = "GOGGOTEST"
	testVersion = 3
)

func testObfuscation() Obfuscator { return XOR("secretfile-test-key") }

// headerLenForTest is magic + version + length, the part of the file that
// describes the rest of it.
func headerLenForTest() int { return len(testMagic) + 1 + 4 }

func TestRoundTrip(t *testing.T) {
	cases := []struct {
		name    string
		payload []byte
	}{
		{"empty", []byte{}},
		{"one byte", []byte{0x00}},
		{"text", []byte("a payload that is not a JSON object")},
		{"NUL bytes and UTF-8", []byte("a\x00b пример рф ¥")},
		{"longer than the key", bytes.Repeat([]byte("0123456789"), 64)},
		{"shorter than the key", []byte("xy")},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			data := Encode(testMagic, testVersion, testObfuscation(), c.payload)
			got, err := Decode(testMagic, testVersion, testObfuscation(), data)
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if !bytes.Equal(got, c.payload) {
				t.Errorf("payload = %q, want %q", got, c.payload)
			}
		})
	}
}

// TestEncodedLayout locks the documented layout, because a caller's magic and
// version are part of a file already on disk: magic | version | length |
// obfuscated payload | crc32 of the stored bytes.
func TestEncodedLayout(t *testing.T) {
	payload := []byte("payload")
	data := Encode(testMagic, testVersion, testObfuscation(), payload)
	hl := headerLenForTest()

	if len(data) != hl+len(payload)+crcLen {
		t.Fatalf("len = %d, want %d", len(data), hl+len(payload)+crcLen)
	}
	if string(data[:len(testMagic)]) != testMagic {
		t.Errorf("file starts with %q, want the magic", data[:len(testMagic)])
	}
	if data[len(testMagic)] != testVersion {
		t.Errorf("version byte = %d, want %d", data[len(testMagic)], testVersion)
	}
	if got := binary.BigEndian.Uint32(data[len(testMagic)+1:]); int(got) != len(payload) {
		t.Errorf("declared length = %d, want %d", got, len(payload))
	}
	stored := data[hl : hl+len(payload)]
	if got, want := binary.BigEndian.Uint32(data[hl+len(payload):]), crc32.ChecksumIEEE(stored); got != want {
		t.Errorf("checksum = %d, want %d", got, want)
	}
}

// TestTruncatedHeader covers the file that does not even hold a header: there
// is nothing to compare yet, so this is its own failure rather than a bad
// magic.
func TestTruncatedHeader(t *testing.T) {
	full := Encode(testMagic, testVersion, testObfuscation(), []byte("payload"))
	for n := 0; n < headerLenForTest(); n++ {
		if _, err := Decode(testMagic, testVersion, testObfuscation(), full[:n]); !errors.Is(err, ErrTruncated) {
			t.Errorf("Decode of %d bytes = %v, want ErrTruncated", n, err)
		}
	}
}

func TestMagicMismatch(t *testing.T) {
	data := Encode(testMagic, testVersion, testObfuscation(), []byte("payload"))
	data[0] ^= 0xff
	if _, err := Decode(testMagic, testVersion, testObfuscation(), data); !errors.Is(err, ErrMagic) {
		t.Errorf("Decode = %v, want ErrMagic", err)
	}
	// The same file read by a caller that names its kind differently: a magic
	// is what keeps two file kinds from being read as each other.
	if _, err := Decode("GOGGOOTHER", testVersion, testObfuscation(), data); !errors.Is(err, ErrMagic) {
		t.Errorf("Decode with another magic = %v, want ErrMagic", err)
	}
}

func TestVersionMismatch(t *testing.T) {
	data := Encode(testMagic, testVersion, testObfuscation(), []byte("payload"))
	if _, err := Decode(testMagic, testVersion+1, testObfuscation(), data); !errors.Is(err, ErrVersion) {
		t.Errorf("Decode = %v, want ErrVersion", err)
	}
}

// TestLengthMismatch covers every file whose declared length does not describe
// it: a trailing byte, a file cut short, and a payload cut short. The last one
// is the case that must NOT be reported as a checksum failure — the length is
// checked first, so a truncated file keeps the reason it failed.
func TestLengthMismatch(t *testing.T) {
	payload := []byte("payload")
	data := Encode(testMagic, testVersion, testObfuscation(), payload)
	hl := headerLenForTest()

	cases := []struct {
		name string
		data []byte
	}{
		{"a trailing byte", append(append([]byte{}, data...), 0x00)},
		{"the checksum cut off", data[:len(data)-1]},
		{"the payload cut short", data[:hl+len(payload)-1]},
		{"the whole payload gone", data[:hl]},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Decode(testMagic, testVersion, testObfuscation(), c.data)
			if !errors.Is(err, ErrLength) {
				t.Errorf("Decode = %v, want ErrLength", err)
			}
			if errors.Is(err, ErrCRC) {
				t.Error("a length failure must not be reported as a checksum failure")
			}
		})
	}
}

// TestCRCMismatch covers a file of the right length whose stored bytes changed:
// the one failure that means the content is not what was written.
func TestCRCMismatch(t *testing.T) {
	payload := []byte("payload")
	data := Encode(testMagic, testVersion, testObfuscation(), payload)
	hl := headerLenForTest()

	for _, i := range []int{hl, hl + len(payload) - 1, len(data) - 1} {
		corrupt := append([]byte{}, data...)
		corrupt[i] ^= 0x01
		if _, err := Decode(testMagic, testVersion, testObfuscation(), corrupt); !errors.Is(err, ErrCRC) {
			t.Errorf("Decode of a file corrupted at byte %d = %v, want ErrCRC", i, err)
		}
	}
}

// TestCheckOrderIsStable pins which failure a file with several problems
// reports: the checks run in the order their failures can be told apart, so a
// file that is both truncated and misnamed is reported as misnamed.
func TestCheckOrderIsStable(t *testing.T) {
	data := Encode(testMagic, testVersion, testObfuscation(), []byte("payload"))

	misnamed := append([]byte{}, data...)
	misnamed[0] ^= 0xff
	misnamed = misnamed[:len(misnamed)-1]
	if _, err := Decode(testMagic, testVersion, testObfuscation(), misnamed); !errors.Is(err, ErrMagic) {
		t.Errorf("Decode = %v, want ErrMagic (magic before length)", err)
	}

	staleVersion := append([]byte{}, data...)
	staleVersion[len(testMagic)]++
	staleVersion = staleVersion[:len(staleVersion)-1]
	if _, err := Decode(testMagic, testVersion, testObfuscation(), staleVersion); !errors.Is(err, ErrVersion) {
		t.Errorf("Decode = %v, want ErrVersion (version before length)", err)
	}
}

// TestTheFramingDoesNotInterpretThePayload is the boundary this package exists
// for: bytes that no caller would accept still round-trip here, because what a
// payload means belongs to the caller. A framing error is about the container,
// never about what the container holds.
func TestTheFramingDoesNotInterpretThePayload(t *testing.T) {
	payload := []byte("this is not a payload any caller understands")
	data := Encode(testMagic, testVersion, testObfuscation(), payload)

	got, err := Decode(testMagic, testVersion, testObfuscation(), data)
	if err != nil {
		t.Fatalf("Decode = %v, want the framing to accept a payload it cannot read", err)
	}

	// A caller's own parse of the same bytes is where such a payload fails,
	// with the caller's own error.
	errCaller := errors.New("caller: payload is malformed")
	parse := func(b []byte) error {
		if !bytes.HasPrefix(b, []byte("caller's own format:")) {
			return errCaller
		}
		return nil
	}
	if err := parse(got); !errors.Is(err, errCaller) {
		t.Errorf("the caller's parse = %v, want its own error", err)
	}
}

// TestXORIsItsOwnInverse locks the Obfuscator contract the framing relies on:
// the same value is applied in both directions, and each key is its own.
func TestXORIsItsOwnInverse(t *testing.T) {
	payload := []byte("a payload with \x00 bytes and UTF-8: пример")

	for _, key := range []string{"k", "goggo-credential-store-v1", "goggo-cookie-store-v1"} {
		obf := XOR(key)
		if got := obf(obf(payload)); !bytes.Equal(got, payload) {
			t.Errorf("key %q: applying the obfuscator twice = %q, want the payload", key, got)
		}
	}

	one, other := XOR("goggo-credential-store-v1"), XOR("goggo-cookie-store-v1")
	if bytes.Equal(one(payload), other(payload)) {
		t.Error("two keys produced the same bytes: a payload encoding is not per caller")
	}
}
