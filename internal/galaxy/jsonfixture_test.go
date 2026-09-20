package galaxy

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

// rawOf converts any fixture value into one raw member.
func rawOf(v any) jsontext.Value {
	raw, err := jsonv2.Marshal(v)
	if err != nil {
		panic(err)
	}
	return jsontext.Value(raw)
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

// mustInt reads an integer member, with mustText's failure rule.
func mustInt(t *testing.T, v jsontext.Value) int64 {
	t.Helper()
	n, err := jsonread.Int(v)
	if err != nil {
		t.Fatalf("jsonread.Int(%s): %v", string(v), err)
	}
	return n
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

// mustArray reads an array member, with mustText's failure rule.
func mustArray(t *testing.T, v jsontext.Value) []jsontext.Value {
	t.Helper()
	a, err := jsonread.Array(v)
	if err != nil {
		t.Fatalf("jsonread.Array(%s): %v", string(v), err)
	}
	return a
}
