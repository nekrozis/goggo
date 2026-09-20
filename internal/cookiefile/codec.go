package cookiefile

import (
	"encoding/binary"
	"errors"
	"time"
)

// The flag byte of a record carries the three booleans a cookie has and
// nothing else, so a bit outside this set means the payload was not written by
// this codec.
const (
	flagSecure   = 1 << 0
	flagHttpOnly = 1 << 1
	flagHostOnly = 1 << 2

	knownFlags = flagSecure | flagHttpOnly | flagHostOnly
)

// recordHeaderLen is flags plus expires: the fixed part of a record, read
// before its four length-prefixed fields.
const recordHeaderLen = 1 + 8

// ErrMalformedPayload is this layer's own failure: the payload is not a record
// sequence. It is deliberately not one of secretfile's framing errors, which
// describe the container rather than the records inside it.
var ErrMalformedPayload = errors.New("cookie payload is malformed")

// PersistentCookie is one cookie as carried by the cookie store file.
//
// Domain is kept verbatim (leading dot and case preserved) in both
// directions: this codec does not normalise domain semantics — that is the
// bridge's job (see doc.go). HostOnly is an explicit flag of the record, so
// unlike a Netscape row the domain never encodes it.
type PersistentCookie struct {
	Expires  time.Time
	Domain   string
	Path     string
	Name     string
	Value    string
	Secure   bool
	HttpOnly bool
	HostOnly bool
}

// Encode serialises cookies into the payload: one record per cookie, in the
// order given, with no count field — the payload length is the single source of
// truth for how many records there are.
//
// A record is:
//
//	flags(1)   bit0=secure  bit1=httpOnly  bit2=hostOnly
//	expires(8) int64 Unix seconds; 0 means a session cookie
//	domain     uint32 big-endian length + UTF-8 bytes
//	path       uint32 big-endian length + UTF-8 bytes
//	name       uint32 big-endian length + UTF-8 bytes
//	value      uint32 big-endian length + UTF-8 bytes
//
// Every byte sequence a cookie can hold is representable, so no cookie is ever
// skipped here.
func Encode(cookies []PersistentCookie) []byte {
	var out []byte
	for _, c := range cookies {
		var flags byte
		if c.Secure {
			flags |= flagSecure
		}
		if c.HttpOnly {
			flags |= flagHttpOnly
		}
		if c.HostOnly {
			flags |= flagHostOnly
		}
		out = append(out, flags)

		var expires int64
		if !c.Expires.IsZero() {
			expires = c.Expires.Unix()
		}
		out = binary.BigEndian.AppendUint64(out, uint64(expires))

		for _, field := range []string{c.Domain, c.Path, c.Name, c.Value} {
			out = binary.BigEndian.AppendUint32(out, uint32(len(field)))
			out = append(out, field...)
		}
	}
	return out
}

// Decode reads a payload back into its records.
//
// An empty payload is an empty cookie set, not a failure. Anything else that is
// not a record sequence is ErrMalformedPayload: a record cut off by the end of
// the payload, a flag byte with a bit this codec does not define, or a field
// length that runs past the end.
func Decode(payload []byte) ([]PersistentCookie, error) {
	r := recordReader{data: payload}
	var out []PersistentCookie
	for r.remaining() > 0 {
		c, err := r.record()
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// recordReader walks a payload field by field, so every bounds check sits in
// one place and a short read is always the same error.
type recordReader struct {
	data []byte
	pos  int
}

func (r *recordReader) remaining() int { return len(r.data) - r.pos }

// take consumes n bytes, or reports the payload as malformed when fewer remain.
func (r *recordReader) take(n int) ([]byte, error) {
	if n < 0 || n > r.remaining() {
		return nil, ErrMalformedPayload
	}
	b := r.data[r.pos : r.pos+n]
	r.pos += n
	return b, nil
}

// field reads one length-prefixed field.
func (r *recordReader) field() (string, error) {
	raw, err := r.take(4)
	if err != nil {
		return "", err
	}
	b, err := r.take(int(binary.BigEndian.Uint32(raw)))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// record reads the next record.
func (r *recordReader) record() (PersistentCookie, error) {
	head, err := r.take(recordHeaderLen)
	if err != nil {
		return PersistentCookie{}, err
	}
	flags := head[0]
	if flags&^knownFlags != 0 {
		return PersistentCookie{}, ErrMalformedPayload
	}

	var c PersistentCookie
	if expires := int64(binary.BigEndian.Uint64(head[1:])); expires != 0 {
		c.Expires = time.Unix(expires, 0)
	}
	c.Secure = flags&flagSecure != 0
	c.HttpOnly = flags&flagHttpOnly != 0
	c.HostOnly = flags&flagHostOnly != 0
	for _, dst := range []*string{&c.Domain, &c.Path, &c.Name, &c.Value} {
		if *dst, err = r.field(); err != nil {
			return PersistentCookie{}, err
		}
	}
	return c, nil
}
