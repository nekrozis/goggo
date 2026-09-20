package core

import (
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"testing"

	"github.com/nekrozis/goggo/internal/jsonread"
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

// mustText reads a text member for an assertion. A fixture that cannot be read
// is a broken test rather than a failed case, so it reports Fatalf.
func mustText(t *testing.T, v jsontext.Value) string {
	t.Helper()
	s, err := jsonread.Text(v)
	if err != nil {
		t.Fatalf("jsonread.Text(%s): %v", string(v), err)
	}
	return s
}

// mustObject reads an object member, with mustText's failure rule.
func mustObject(t *testing.T, v jsontext.Value) map[string]jsontext.Value {
	t.Helper()
	o, err := jsonread.Object(v)
	if err != nil {
		t.Fatalf("jsonread.Object(%s): %v", string(v), err)
	}
	return o
}
