package webapi

import (
	"errors"
	"testing"
)

// TestRequireJSONObjectClassification locks the gate's matrix: exactly one
// complete JSON object passes, and every other body is ErrNotJSON.
//
// Each rejected case carries one of the rules the gate exists for —
//
//	shape       an array and a scalar are not objects
//	one value   a second value and trailing garbage are not part of it
//	well-formed truncation, a repeated member name and invalid UTF-8 are not
//	            JSON this build reads
//	no trim     a whitespace byte JSON does not recognise (U+00A0) is refused
//	            rather than trimmed away, which is what the four JSON
//	            whitespace bytes around the object show by contrast
//
// — so a case that starts passing names the rule that broke. The whitespace-only
// body is the one rejection that is not a parse failure of a value: there is no
// value in it at all.
func TestRequireJSONObjectClassification(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{name: "object", body: `{"title":"Alpha","dlcs":[{"manualUrl":"x"}]}`},
		{name: "object with JSON whitespace around it", body: "  \t{\"a\":1}\n "},
		{name: "array", body: `[1,2]`, wantErr: true},
		{name: "scalar", body: `42`, wantErr: true},
		{name: "empty", body: ``, wantErr: true},
		{name: "whitespace only", body: "   ", wantErr: true},
		{name: "truncated", body: `{"a":1`, wantErr: true},
		{name: "two values", body: `{"a":1}{"b":2}`, wantErr: true},
		{name: "trailing garbage", body: `{"a":1} x`, wantErr: true},
		{name: "duplicate member", body: `{"a":1,"a":2}`, wantErr: true},
		{name: "invalid UTF-8", body: "{\"a\":\"\xff\xfe\"}", wantErr: true},
		{name: "NBSP prefix", body: "\u00a0{\"a\":1}", wantErr: true},
		{name: "NBSP suffix", body: "{\"a\":1}\u00a0", wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := requireJSONObject([]byte(c.body))
			if !c.wantErr {
				if err != nil {
					t.Fatalf("requireJSONObject(%q) = %v, want accepted", c.body, err)
				}
				return
			}
			if !errors.Is(err, ErrNotJSON) {
				t.Errorf("requireJSONObject(%q) = %v, want ErrNotJSON", c.body, err)
			}
		})
	}
}

// TestDecodeObjectKeepsTheOriginalBytes locks the same-class defect this round
// closed. decodeObject used to trim the body and hand the TRIMMED bytes to the
// decoder, so a U+00A0 prefix was swallowed and a document the input boundary
// must refuse was accepted. Both ends of that are asserted here: the byte JSON
// does not recognise is refused, the bytes it does accept still arrive.
func TestDecodeObjectKeepsTheOriginalBytes(t *testing.T) {
	t.Run("non-JSON whitespace is refused", func(t *testing.T) {
		for _, in := range []string{"\u00a0{\"a\":1}", "{\"a\":1}\u00a0"} {
			var obj map[string]any
			if err := decodeObject([]byte(in), &obj); !errors.Is(err, ErrNotJSON) {
				t.Errorf("decodeObject(%q) = %v, want ErrNotJSON", in, err)
			}
		}
	})

	t.Run("JSON whitespace still arrives", func(t *testing.T) {
		var obj map[string]any
		if err := decodeObject([]byte("  {\"a\":1}\n"), &obj); err != nil {
			t.Fatalf("decodeObject with JSON whitespace = %v, want accepted", err)
		}
	})

	t.Run("the shape decision is the gate's", func(t *testing.T) {
		for _, in := range []string{`[1,2]`, `{"a":`, `{"a":1}{"b":2}`} {
			var obj map[string]any
			if err := decodeObject([]byte(in), &obj); !errors.Is(err, ErrNotJSON) {
				t.Errorf("decodeObject(%s) = %v, want ErrNotJSON", in, err)
			}
		}
	})
}
