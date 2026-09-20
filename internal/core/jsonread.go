package core

import (
	"bytes"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"fmt"
	"math"
)

// This file holds the JSON readers of the core package.
//
// There is deliberately no shared reader package: a member's rule belongs to the
// code that consumes it, and the rules differ from package to package.
//
// A member is carried as a jsontext.Value, the raw JSON text the document
// contained, which is what lets a number keep the literal the server sent
// instead of being flattened into a float64 on the way in. A member that is not
// in the document reads as the zero jsontext.Value; the readers below treat
// that the same as a null, because the documents this program reads make no
// distinction between the two except where a reader says so.

// maxInt64Exclusive is 2^63 as a float64: the first value out of int64 range.
// float64(math.MaxInt64) rounds up to exactly this value, so the comparison has
// to be >=. The float fallback of memberInt compares against it.
const maxInt64Exclusive = float64(1 << 63)

// jsonKind names a decoded value for error messages.
func jsonKind(v jsontext.Value) string {
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

// tokenOf decodes the single token a bare member holds.
func tokenOf(v jsontext.Value) (jsontext.Token, error) {
	tok, err := jsontext.NewDecoder(bytes.NewReader(v)).ReadToken()
	if err != nil {
		return jsontext.Token{}, fmt.Errorf("invalid JSON: %w", err)
	}
	return tok, nil
}

// memberText reads a scalar member as text: a missing member and a null one are
// "", a string gives its unescaped value, and a number or a boolean gives the
// literal the document carried. A container has no text form and is an error.
func memberText(v jsontext.Value) (string, error) {
	switch v.Kind() {
	case jsontext.KindInvalid, jsontext.KindNull:
		return "", nil
	case jsontext.KindString, jsontext.KindNumber, jsontext.KindTrue, jsontext.KindFalse:
		tok, err := tokenOf(v)
		if err != nil {
			return "", err
		}
		return tok.String(), nil
	default:
		return "", fmt.Errorf("expected a scalar, got %s", jsonKind(v))
	}
}

// stringOnly reads a member that has to be a JSON string: a missing member and
// a null one are "", and any other shape is an error. It is the strict
// counterpart of memberText, for fields where anything but a string would be a
// protocol error rather than a value.
func stringOnly(v jsontext.Value) (string, error) {
	switch v.Kind() {
	case jsontext.KindInvalid, jsontext.KindNull:
		return "", nil
	case jsontext.KindString:
		tok, err := tokenOf(v)
		if err != nil {
			return "", err
		}
		return tok.String(), nil
	default:
		return "", fmt.Errorf("expected a JSON string, got %s", jsonKind(v))
	}
}

// memberInt reads an integer member: a missing member and a null one are 0, a
// boolean is 0/1, and a number must be whole and inside int64 range.
//
// The token reader refuses a literal that carries a fraction or an exponent
// ("12.0", "1e2"), so a whole value the API wrote that way is retried through
// the float form; a non-whole or out-of-range literal is an error, never a
// saturated value.
func memberInt(v jsontext.Value) (int64, error) {
	switch v.Kind() {
	case jsontext.KindInvalid, jsontext.KindNull:
		return 0, nil
	case jsontext.KindTrue:
		return 1, nil
	case jsontext.KindFalse:
		return 0, nil
	case jsontext.KindNumber:
		tok, err := tokenOf(v)
		if err != nil {
			return 0, err
		}
		if n, err := tok.Int(); err == nil {
			return n, nil
		}
		f, err := tok.Float()
		if err != nil {
			return 0, fmt.Errorf("expected an integer, got %s", tok.String())
		}
		if math.IsNaN(f) || math.IsInf(f, 0) || f != math.Trunc(f) {
			return 0, fmt.Errorf("expected an integer, got %s", tok.String())
		}
		if f >= maxInt64Exclusive || f < -maxInt64Exclusive {
			return 0, fmt.Errorf("integer %s overflows int64", tok.String())
		}
		return int64(f), nil
	default:
		return 0, fmt.Errorf("expected an integer, got %s", jsonKind(v))
	}
}

// memberFloat reads a number member as a float: a missing member and a null one
// are 0 and a boolean is 0/1. A string is an error, because a numeric string is
// a different thing from a number.
func memberFloat(v jsontext.Value) (float64, error) {
	switch v.Kind() {
	case jsontext.KindInvalid, jsontext.KindNull:
		return 0, nil
	case jsontext.KindTrue:
		return 1, nil
	case jsontext.KindFalse:
		return 0, nil
	case jsontext.KindNumber:
		tok, err := tokenOf(v)
		if err != nil {
			return 0, err
		}
		f, err := tok.Float()
		if err != nil {
			return 0, fmt.Errorf("expected a number, got %s", jsonKind(v))
		}
		return f, nil
	default:
		return 0, fmt.Errorf("expected a number, got %s", jsonKind(v))
	}
}

// memberBool reads a boolean member: a missing member and a null one are false
// and a number is a test against zero. A string is an error — the word "true"
// is not a boolean the API sent.
func memberBool(v jsontext.Value) (bool, error) {
	switch v.Kind() {
	case jsontext.KindInvalid, jsontext.KindNull:
		return false, nil
	case jsontext.KindTrue:
		return true, nil
	case jsontext.KindFalse:
		return false, nil
	case jsontext.KindNumber:
		f, err := memberFloat(v)
		if err != nil {
			return false, err
		}
		return f != 0, nil
	default:
		return false, fmt.Errorf("expected a bool, got %s", jsonKind(v))
	}
}

// memberObject reads an object member. A missing member and a null one are nil
// — "not present" — while any other shape is an error, because a document that
// carries the section in the wrong shape is broken rather than empty.
func memberObject(v jsontext.Value) (map[string]jsontext.Value, error) {
	if v.Kind() == jsontext.KindInvalid || v.Kind() == jsontext.KindNull {
		return nil, nil
	}
	if v.Kind() != jsontext.KindBeginObject {
		return nil, fmt.Errorf("expected a JSON object, got %s", jsonKind(v))
	}
	var obj map[string]jsontext.Value
	if err := jsonv2.Unmarshal(v, &obj); err != nil {
		return nil, err
	}
	return obj, nil
}

// memberArray reads an array member, with memberObject's absent/null rule.
func memberArray(v jsontext.Value) ([]jsontext.Value, error) {
	if v.Kind() == jsontext.KindInvalid || v.Kind() == jsontext.KindNull {
		return nil, nil
	}
	if v.Kind() != jsontext.KindBeginArray {
		return nil, fmt.Errorf("expected a JSON array, got %s", jsonKind(v))
	}
	var arr []jsontext.Value
	if err := jsonv2.Unmarshal(v, &arr); err != nil {
		return nil, err
	}
	return arr, nil
}

// rawArray renders members as the raw text of one JSON array, for putting a
// list this program assembled back into a document of raw members.
func rawArray(elems []jsontext.Value) jsontext.Value {
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
