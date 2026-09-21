package webapi

import (
	"errors"
	"testing"
)

// TestRequireJSONObjectClassification locks the gate's matrix: exactly one
// complete JSON object passes, and every other body is ErrNotJSON.
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

// TestDecodeObjectKeepsTheOriginalBytes verifies that non-JSON whitespace is
// refused while valid JSON whitespace around the object is accepted.
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
