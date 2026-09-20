// Package jsonread provides the readers this program inspects a decoded JSON
// document with.
//
// A document is carried as map[string]jsontext.Value: each member keeps the raw
// JSON text the server sent, so a number keeps its literal form instead of being
// flattened into a float64 on the way in. That matters for the identifier
// family, where the same field arrives as a string in one entry of a vector and
// as a number in the next, and where a value wider than 2^53 must not lose its
// low digits.
//
// These readers are deliberately NOT one lenient conversion. A caller states how
// wide it means to read, and the name says which width it gets:
//
//	Text    a JSON string, and nothing else
//	Scalar  any scalar, as the literal text the document used
//	Int     a whole number inside int64
//	Uint    a whole number inside uint64
//	Float   a number
//	Bool    a boolean
//
// A field that has to be text therefore cannot silently become "true" or "123":
// Text refuses those, and the caller that wants the wider read has to name
// Scalar. This split is the point of the package — a single Str-style reader
// that coerced everything was how a protocol error turned into a plausible value
// and how the strictness of a read stopped being visible at the call site.
//
// A member that is not in the document reads as the zero jsontext.Value, which
// every reader here treats the same as a null: the documents this program reads
// make no distinction between the two.
package jsonread

import (
	"bytes"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"fmt"
	"math"
)

// maxInt64Exclusive is 2^63 as a float64: the first value out of int64 range.
// float64(math.MaxInt64) rounds up to exactly this value, so the comparison has
// to be >=. The float fallback of Int compares against it.
const maxInt64Exclusive = float64(1 << 63)

// maxUint64Exclusive is 2^64 — the first value past uint64. The float fallback
// of Uint compares against it.
const maxUint64Exclusive = float64(1 << 64)

// Kind names a decoded value for error messages. A member that is not in the
// document is "missing" and a null is "null", because the two are different
// things to a reader even though most readers treat them alike.
func Kind(v jsontext.Value) string {
	switch v.Kind() {
	case jsontext.KindInvalid:
		return "missing"
	case jsontext.KindNull:
		return "null"
	case jsontext.KindString:
		return "string"
	case jsontext.KindNumber:
		return "number"
	case jsontext.KindTrue, jsontext.KindFalse:
		return "boolean"
	case jsontext.KindBeginObject:
		return "object"
	case jsontext.KindBeginArray:
		return "array"
	default:
		return "invalid"
	}
}

// scalar reports whether v is a JSON scalar — a string, a number or a boolean —
// as opposed to a container, a null or a member that is not there.
func scalar(v jsontext.Value) bool {
	switch v.Kind() {
	case jsontext.KindString, jsontext.KindNumber, jsontext.KindTrue, jsontext.KindFalse:
		return true
	default:
		return false
	}
}

// token decodes the single token a bare scalar member holds. The token, not the
// raw text, is what answers for a scalar: its String() form is the unescaped
// string value and its Int()/Uint()/Float() forms are the typed ones.
//
// The typed accessors PANIC on a token of the wrong kind, so a caller must check
// the kind before calling one.
func token(v jsontext.Value) (jsontext.Token, error) {
	tok, err := jsontext.NewDecoder(bytes.NewReader(v)).ReadToken()
	if err != nil {
		return jsontext.Token{}, fmt.Errorf("invalid JSON: %w", err)
	}
	return tok, nil
}

// Text reads a member that has to be a JSON string: a string gives its unescaped
// value, and a missing member and a null one are "". Every other shape is an
// error — a number, a boolean and a container have no text form here.
//
// This is the reader for a field whose JSON type is fixed: a path, a hash, a url,
// a name. Use Scalar instead where the documents really do vary the shape.
func Text(v jsontext.Value) (string, error) {
	switch v.Kind() {
	case jsontext.KindInvalid, jsontext.KindNull:
		return "", nil
	case jsontext.KindString:
		tok, err := token(v)
		if err != nil {
			return "", err
		}
		return tok.String(), nil
	default:
		return "", fmt.Errorf("expected a JSON string, got %s", Kind(v))
	}
}

// Scalar reads a scalar member as text: a string gives its unescaped value and a
// number or a boolean gives the literal the document carried. A missing member
// and a null one are "", and a container is an error.
//
// It is the wider read, for the fields the API is known to vary: an identifier
// that is sometimes a number, and the handful of fields an upstream rule reads
// as "number when it is one, text otherwise". A number keeps its literal text
// rather than going through a float64, so a wide identifier does not lose digits.
func Scalar(v jsontext.Value) (string, error) {
	if v.Kind() == jsontext.KindInvalid || v.Kind() == jsontext.KindNull {
		return "", nil
	}
	if !scalar(v) {
		return "", fmt.Errorf("expected a scalar, got %s", Kind(v))
	}
	tok, err := token(v)
	if err != nil {
		return "", err
	}
	return tok.String(), nil
}

// Int reads a whole number inside int64: a missing member and a null one are 0.
//
// The token accessor refuses a literal carrying a fraction or an exponent
// ("12.0", "1e2"), so a whole value written that way is retried through the float
// form; a non-whole or out-of-range literal is an error rather than a saturated
// value. A string and a boolean are errors: a numeric string is a different thing
// from a number, and a boolean is not a count.
func Int(v jsontext.Value) (int64, error) {
	switch v.Kind() {
	case jsontext.KindInvalid, jsontext.KindNull:
		return 0, nil
	case jsontext.KindNumber:
		tok, err := token(v)
		if err != nil {
			return 0, err
		}
		if n, err := tok.Int(); err == nil {
			return n, nil
		}
		f, err := tok.Float()
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f != math.Trunc(f) {
			return 0, fmt.Errorf("expected an integer, got %s", tok.String())
		}
		if f >= maxInt64Exclusive || f < -maxInt64Exclusive {
			return 0, fmt.Errorf("integer %s overflows int64", tok.String())
		}
		return int64(f), nil
	default:
		return 0, fmt.Errorf("expected an integer, got %s", Kind(v))
	}
}

// Uint reads a whole number inside uint64, with Int's missing/null and
// whole-number rules. A negative value is an error, so a size or an offset can
// never wrap around.
func Uint(v jsontext.Value) (uint64, error) {
	switch v.Kind() {
	case jsontext.KindInvalid, jsontext.KindNull:
		return 0, nil
	case jsontext.KindNumber:
		tok, err := token(v)
		if err != nil {
			return 0, err
		}
		if n, err := tok.Uint(); err == nil {
			return n, nil
		}
		f, err := tok.Float()
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f != math.Trunc(f) {
			return 0, fmt.Errorf("expected an unsigned integer, got %s", tok.String())
		}
		if f < 0 || f >= maxUint64Exclusive {
			return 0, fmt.Errorf("integer %s is outside the uint64 range", tok.String())
		}
		return uint64(f), nil
	default:
		return 0, fmt.Errorf("expected an unsigned integer, got %s", Kind(v))
	}
}

// Float reads a number: a missing member and a null one are 0. A string and a
// boolean are errors, for the same reasons as Int's.
func Float(v jsontext.Value) (float64, error) {
	switch v.Kind() {
	case jsontext.KindInvalid, jsontext.KindNull:
		return 0, nil
	case jsontext.KindNumber:
		tok, err := token(v)
		if err != nil {
			return 0, err
		}
		f, err := tok.Float()
		if err != nil {
			return 0, fmt.Errorf("expected a number, got %s", tok.String())
		}
		return f, nil
	default:
		return 0, fmt.Errorf("expected a number, got %s", Kind(v))
	}
}

// Bool reads a boolean: a missing member and a null one are false. A number and
// a string are errors — the word "true" is not a boolean the API sent, and a
// count is not a flag.
func Bool(v jsontext.Value) (bool, error) {
	switch v.Kind() {
	case jsontext.KindInvalid, jsontext.KindNull:
		return false, nil
	case jsontext.KindTrue:
		return true, nil
	case jsontext.KindFalse:
		return false, nil
	default:
		return false, fmt.Errorf("expected a bool, got %s", Kind(v))
	}
}

// Object reads a member that has to be a JSON object: a missing member and a
// null one are nil — "not present" — while any other shape is an error, because
// a document that carries a section in the wrong shape is broken rather than
// empty.
func Object(v jsontext.Value) (map[string]jsontext.Value, error) {
	if v.Kind() == jsontext.KindInvalid || v.Kind() == jsontext.KindNull {
		return nil, nil
	}
	if v.Kind() != jsontext.KindBeginObject {
		return nil, fmt.Errorf("expected a JSON object, got %s", Kind(v))
	}
	var obj map[string]jsontext.Value
	if err := jsonv2.Unmarshal(v, &obj); err != nil {
		return nil, err
	}
	return obj, nil
}

// Array reads an array member, with Object's missing/null rule.
func Array(v jsontext.Value) ([]jsontext.Value, error) {
	if v.Kind() == jsontext.KindInvalid || v.Kind() == jsontext.KindNull {
		return nil, nil
	}
	if v.Kind() != jsontext.KindBeginArray {
		return nil, fmt.Errorf("expected a JSON array, got %s", Kind(v))
	}
	var arr []jsontext.Value
	if err := jsonv2.Unmarshal(v, &arr); err != nil {
		return nil, err
	}
	return arr, nil
}

// RawArray renders members as the raw text of one JSON array, for putting a list
// this program assembled back into a document that carries raw members.
func RawArray(elems []jsontext.Value) jsontext.Value {
	out := make([]byte, 0, 2)
	out = append(out, '[')
	for i, e := range elems {
		if i > 0 {
			out = append(out, ',')
		}
		out = append(out, e...)
	}
	return jsontext.Value(append(out, ']'))
}
