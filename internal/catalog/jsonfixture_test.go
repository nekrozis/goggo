package catalog

import (
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
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
