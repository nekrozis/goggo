package gamedetails

import (
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"testing"
)

// Fixtures in this package are written as ordinary Go value trees, because that
// is what makes them readable. These helpers convert one into the raw-member
// form the package's readers take, so the conversion happens in one place
// instead of at every fixture.

// docOf converts an object fixture.
func docOf(m map[string]any) map[string]jsontext.Value {
	raw, err := jsonv2.Marshal(m)
	if err != nil {
		panic(err)
	}
	var out map[string]jsontext.Value
	if err := jsonv2.Unmarshal(raw, &out); err != nil {
		panic(err)
	}
	return out
}

// valuesOf converts an array fixture.
func valuesOf(v []any) []jsontext.Value {
	raw, err := jsonv2.Marshal(v)
	if err != nil {
		panic(err)
	}
	var out []jsontext.Value
	if err := jsonv2.Unmarshal(raw, &out); err != nil {
		panic(err)
	}
	return out
}

// rawOf converts any fixture value into one raw member.
func rawOf(v any) jsontext.Value {
	raw, err := jsonv2.Marshal(v)
	if err != nil {
		panic(err)
	}
	return jsontext.Value(raw)
}

// mustText reads a member for an assertion. A fixture that cannot be read is a
// broken test rather than a failed case, so it reports Fatalf.
func mustText(t *testing.T, v jsontext.Value) string {
	t.Helper()
	s, err := memberText(v)
	if err != nil {
		t.Fatalf("memberText(%s): %v", string(v), err)
	}
	return s
}

// mustInt reads an integer member, with mustText's failure rule.
func mustInt(t *testing.T, v jsontext.Value) int64 {
	t.Helper()
	n, err := memberInt(v)
	if err != nil {
		t.Fatalf("memberInt(%s): %v", string(v), err)
	}
	return n
}

// mustFloat reads a number member, with mustText's failure rule.
func mustFloat(t *testing.T, v jsontext.Value) float64 {
	t.Helper()
	f, err := memberFloat(v)
	if err != nil {
		t.Fatalf("memberFloat(%s): %v", string(v), err)
	}
	return f
}

// mustBool reads a boolean member, with mustText's failure rule.
func mustBool(t *testing.T, v jsontext.Value) bool {
	t.Helper()
	b, err := memberBool(v)
	if err != nil {
		t.Fatalf("memberBool(%s): %v", string(v), err)
	}
	return b
}

// mustObject reads an object member, with mustText's failure rule.
func mustObject(t *testing.T, v jsontext.Value) map[string]jsontext.Value {
	t.Helper()
	o, err := memberObject(v)
	if err != nil {
		t.Fatalf("memberObject(%s): %v", string(v), err)
	}
	return o
}

// mustArray reads an array member, with mustText's failure rule.
func mustArray(t *testing.T, v jsontext.Value) []jsontext.Value {
	t.Helper()
	a, err := memberArray(v)
	if err != nil {
		t.Fatalf("memberArray(%s): %v", string(v), err)
	}
	return a
}
