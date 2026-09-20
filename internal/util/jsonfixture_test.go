package util

import (
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
)

// Fixtures in this package are written as ordinary Go value trees, because that
// is what makes them readable. rawOf converts one into the raw-member form the
// package's readers take, so the conversion happens in one place instead of at
// every fixture.
func rawOf(v any) jsontext.Value {
	raw, err := jsonv2.Marshal(v)
	if err != nil {
		panic(err)
	}
	return jsontext.Value(raw)
}
