package cli

import (
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"testing"
)

// docOf converts a fixture written as an ordinary Go value tree into the
// raw-member form the core package's documents take.
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

// mustText reads a member for an assertion.
func mustText(t *testing.T, v jsontext.Value) string {
	t.Helper()
	var s string
	if err := jsonv2.Unmarshal(v, &s); err != nil {
		t.Fatalf("read %s: %v", string(v), err)
	}
	return s
}
