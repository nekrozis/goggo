package cookiefile

import (
	"encoding/binary"
	"errors"
	"testing"
	"time"
)

func pc(domain, path, name, value string, hostOnly bool) PersistentCookie {
	return PersistentCookie{
		Domain:   domain,
		Path:     path,
		Name:     name,
		Value:    value,
		HostOnly: hostOnly,
	}
}

func checkCookie(t *testing.T, i int, want, got PersistentCookie) {
	t.Helper()
	if got.Domain != want.Domain {
		t.Errorf("record %d: domain = %q, want %q", i, got.Domain, want.Domain)
	}
	if got.Path != want.Path {
		t.Errorf("record %d: path = %q, want %q", i, got.Path, want.Path)
	}
	if got.Name != want.Name {
		t.Errorf("record %d: name = %q, want %q", i, got.Name, want.Name)
	}
	if got.Value != want.Value {
		t.Errorf("record %d: value = %q, want %q", i, got.Value, want.Value)
	}
	if got.Secure != want.Secure {
		t.Errorf("record %d: secure = %v, want %v", i, got.Secure, want.Secure)
	}
	if got.HttpOnly != want.HttpOnly {
		t.Errorf("record %d: httpOnly = %v, want %v", i, got.HttpOnly, want.HttpOnly)
	}
	if got.HostOnly != want.HostOnly {
		t.Errorf("record %d: hostOnly = %v, want %v", i, got.HostOnly, want.HostOnly)
	}
	if !got.Expires.Equal(want.Expires) {
		t.Errorf("record %d: expires = %v, want %v", i, got.Expires, want.Expires)
	}
}

// roundTrip encodes then decodes, so a test states the cookies it cares about
// rather than the payload shape.
func roundTrip(t *testing.T, in []PersistentCookie) []PersistentCookie {
	t.Helper()
	out, err := Decode(Encode(in))
	if err != nil {
		t.Fatalf("Decode(Encode(%+v)): %v", in, err)
	}
	return out
}

func TestRoundTripPreservesEveryField(t *testing.T) {
	in := []PersistentCookie{
		pc("example.com", "/", "sid", "abc123", false),
		pc("www.example.com", "/foo", "host", "v", true),
		{
			Expires:  time.Unix(1700000000, 0),
			Domain:   "secure.test",
			Path:     "/",
			Name:     "tok",
			Value:    "x",
			Secure:   true,
			HttpOnly: true,
			HostOnly: false,
		},
	}

	out := roundTrip(t, in)
	if len(out) != len(in) {
		t.Fatalf("decoded %d records, want %d", len(out), len(in))
	}
	for i := range in {
		checkCookie(t, i, in[i], out[i])
	}
}

// TestDomainKeptVerbatim locks the codec's domain transparency: case and
// leading dots survive byte-for-byte. The host-only flag is a separate field,
// so unlike a Netscape row the domain never carries it. If a future test ever
// expects normalisation here (e.g. "Example.COM" -> "example.com"), that is a
// responsibility leak — domain/host-only/path semantics belong to the httpx
// bridge, never to the format codec.
func TestDomainKeptVerbatim(t *testing.T) {
	cases := []PersistentCookie{
		pc("Example.COM", "/", "n1", "v1", true),
		pc(".Example.COM", "/", "n2", "v2", false),
		pc(".пример.рф", "/", "n3", "v3", true),
	}
	out := roundTrip(t, cases)
	if len(out) != len(cases) {
		t.Fatalf("decoded %d records, want %d", len(out), len(cases))
	}
	for i, want := range cases {
		if out[i].Domain != want.Domain {
			t.Errorf("record %d domain = %q, want %q (must be verbatim)", i, out[i].Domain, want.Domain)
		}
	}
}

// TestSessionExpiryZero locks that a cookie with no expiry is stored as a
// session cookie: it survives the round trip with a zero time rather than an
// invented expiry.
func TestSessionExpiryZero(t *testing.T) {
	out := roundTrip(t, []PersistentCookie{pc("a.com", "/", "s", "1", true)})
	if len(out) != 1 {
		t.Fatalf("decoded %d records, want 1", len(out))
	}
	if !out[0].Expires.IsZero() {
		t.Errorf("session cookie got expires %v", out[0].Expires)
	}
}

func TestEmptyValueIsKept(t *testing.T) {
	out := roundTrip(t, []PersistentCookie{pc("gog.com", "/", "SID", "", false)})
	if len(out) != 1 || out[0].Name != "SID" || out[0].Value != "" {
		t.Errorf("decoded = %+v, want SID with an empty value", out)
	}
}

// TestEmptyCookieSetRoundTrips: no cookies is a valid payload of length zero,
// which is what lets an empty jar be saved as a file rather than as nothing.
func TestEmptyCookieSetRoundTrips(t *testing.T) {
	payload := Encode(nil)
	if len(payload) != 0 {
		t.Errorf("Encode(nil) = %d bytes, want none", len(payload))
	}
	out, err := Decode(payload)
	if err != nil {
		t.Fatalf("Decode of an empty payload: %v, want an empty set", err)
	}
	if len(out) != 0 {
		t.Errorf("decoded %d records, want none", len(out))
	}
}

// TestAnyByteSequenceIsRepresentable is the difference the binary payload makes:
// a TAB, CR, LF or a '#' inside a field is ordinary data here, where the
// text format had to skip such a cookie.
func TestAnyByteSequenceIsRepresentable(t *testing.T) {
	in := []PersistentCookie{
		pc(".пример.рф", "/a b", "k#1", `va lue "quoted" ¥`, false),
		pc("example.com", "/", "tab\tcr\rlf\n", "a\tb\r\nc", true),
	}
	out := roundTrip(t, in)
	if len(out) != len(in) {
		t.Fatalf("decoded %d records, want %d (no record may be skipped)", len(out), len(in))
	}
	for i := range in {
		checkCookie(t, i, in[i], out[i])
	}
}

// TestFlagsAreIndependent covers every combination of the three flag bits: they
// are separate fields of the record, so no one of them may imply another.
func TestFlagsAreIndependent(t *testing.T) {
	var in []PersistentCookie
	for _, secure := range []bool{false, true} {
		for _, httpOnly := range []bool{false, true} {
			for _, hostOnly := range []bool{false, true} {
				c := pc("example.com", "/", "n", "v", hostOnly)
				c.Secure, c.HttpOnly = secure, httpOnly
				in = append(in, c)
			}
		}
	}

	out := roundTrip(t, in)
	if len(out) != len(in) {
		t.Fatalf("decoded %d records, want %d", len(out), len(in))
	}
	for i := range in {
		checkCookie(t, i, in[i], out[i])
	}
}

// TestMalformedPayloadRejected covers the shapes a payload can fail in: the
// error is this layer's own, because the framing was intact in every case.
func TestMalformedPayloadRejected(t *testing.T) {
	valid := Encode([]PersistentCookie{pc("example.com", "/", "SID", "v", true)})

	lengthPastEnd := binary.BigEndian.AppendUint32(nil, 10) // a 10-byte field with nothing behind it
	cases := []struct {
		name    string
		payload []byte
	}{
		{"fewer bytes than a record header", valid[:recordHeaderLen-1]},
		{"a record cut off inside a field", valid[:len(valid)-1]},
		{"a field length that runs past the end",
			append(append([]byte{}, valid[:recordHeaderLen]...), lengthPastEnd...)},
		{"a flag byte with a bit this codec does not define",
			append([]byte{knownFlags | 0x80}, make([]byte, recordHeaderLen-1)...)},
		{"a second record that is only a header",
			append(append([]byte{}, valid...), make([]byte, recordHeaderLen-1)...)},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := Decode(c.payload)
			if !errors.Is(err, ErrMalformedPayload) {
				t.Errorf("Decode = %v, want ErrMalformedPayload", err)
			}
			if out != nil {
				t.Errorf("Decode returned %d records alongside the failure, want none", len(out))
			}
		})
	}
}

// TestDecodeRejectsTruncationRatherThanReturningAPrefix: a payload whose last
// record is incomplete must not be read as "the records that did fit", because
// that would silently drop a cookie instead of reporting the file.
func TestDecodeRejectsTruncationRatherThanReturningAPrefix(t *testing.T) {
	first := pc("a.example", "/", "A", "1", true)
	second := pc("b.example", "/", "B", "2", true)
	full := Encode([]PersistentCookie{first, second})
	oneRecord := len(Encode([]PersistentCookie{first}))

	out, err := Decode(full[:oneRecord+3])
	if !errors.Is(err, ErrMalformedPayload) {
		t.Errorf("Decode = %v, want ErrMalformedPayload", err)
	}
	if out != nil {
		t.Errorf("Decode = %+v, want no records", out)
	}
}
