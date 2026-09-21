package util

import (
	"encoding/json/jsontext"
	"testing"
)

// TestManualURLsFromJSON locks the collection order: arrays keep their element
// order, object members are visited in sorted key order.
func TestManualURLsFromJSON(t *testing.T) {
	t.Run("array keeps order", func(t *testing.T) {
		in := jsontext.Value(`[{"manualUrl":"b"},{"manualUrl":"a"}]`)
		got, err := ManualURLsFromJSON(in)
		if err != nil {
			t.Fatalf("ManualURLsFromJSON: %v", err)
		}
		if len(got) != 2 || got[0] != "b" || got[1] != "a" {
			t.Errorf("urls = %v, want [b a]", got)
		}
	})

	t.Run("object keys are sorted", func(t *testing.T) {
		// The walk visits object members in sorted key order for deterministic output.
		in := jsontext.Value(`{"2":{"manualUrl":"second"},"1":{"manualUrl":"first"},"3":{"manualUrl":"third"}}`)
		got, err := ManualURLsFromJSON(in)
		if err != nil {
			t.Fatalf("ManualURLsFromJSON: %v", err)
		}
		want := []string{"first", "second", "third"}
		if len(got) != len(want) {
			t.Fatalf("urls = %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("urls[%d] = %q, want %q (key order, not document order)", i, got[i], want[i])
			}
		}
	})

	t.Run("nested containers are walked", func(t *testing.T) {
		in := jsontext.Value(`{"outer":[{"inner":{"manualUrl":"deep"}}]}`)
		got, err := ManualURLsFromJSON(in)
		if err != nil {
			t.Fatalf("ManualURLsFromJSON: %v", err)
		}
		if len(got) != 1 || got[0] != "deep" {
			t.Errorf("urls = %v", got)
		}
	})

	t.Run("a manualUrl member is not recursed into", func(t *testing.T) {
		in := jsontext.Value(`{"manualUrl":{"manualUrl":"hidden"}}`)
		if _, err := ManualURLsFromJSON(in); err == nil {
			t.Error("want error for a non-scalar manualUrl")
		}
	})

	t.Run("manualUrl scalar coercion and container rejection", func(t *testing.T) {
		// Numeric cases verify fixed-point float64 rendering rather than lexical spelling.
		cases := []struct {
			name    string
			val     string
			want    string
			wantErr bool
		}{
			{name: "string", val: `"https://example.com/dlc"`, want: "https://example.com/dlc"},
			{name: "integer", val: `12345`, want: "12345"},
			{name: "wide integer", val: `1234567890123`, want: "1234567890123"},
			{name: "fraction", val: `5.25`, want: "5.25"},
			{name: "exponent", val: `1e3`, want: "1000"},
			{name: "negative exponent", val: `1e-7`, want: "0.0000001"},
			{name: "negative zero", val: `-0`, want: "-0"},
			// Beyond float64's precision but inside its exponent range: the
			// rendering is the float64, not the document's literal. Precision
			// preservation belongs to the paths that hand the raw bytes on.
			{name: "integer beyond float64 precision", val: `58812465975493914`, want: "58812465975493910"},
			{name: "number beyond float64 range", val: `1e400`, wantErr: true},
			{name: "bool true", val: `true`, want: "true"},
			{name: "bool false", val: `false`, want: "false"},
			{name: "null", val: `null`, want: ""},
			{name: "array rejected", val: `["a"]`, wantErr: true},
			{name: "object rejected", val: `{"a":1}`, wantErr: true},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				in := jsontext.Value(`{"manualUrl":` + tc.val + `}`)
				got, err := ManualURLsFromJSON(in)
				if tc.wantErr {
					if err == nil {
						t.Fatalf("ManualURLsFromJSON(%s) expected error, got nil", tc.val)
					}
					return
				}
				if err != nil {
					t.Fatalf("ManualURLsFromJSON(%s) unexpected error: %v", tc.val, err)
				}
				if len(got) != 1 || got[0] != tc.want {
					t.Errorf("ManualURLsFromJSON(%s) = %v, want [%q]", tc.val, got, tc.want)
				}
			})
		}
	})

	t.Run("non-containers contribute nothing", func(t *testing.T) {
		for _, in := range []jsontext.Value{
			jsontext.Value("null"),
			jsontext.Value(`"text"`),
			jsontext.Value("1"),
			jsontext.Value("[]"),
			jsontext.Value("{}"),
		} {
			got, err := ManualURLsFromJSON(in)
			if err != nil {
				t.Fatalf("ManualURLsFromJSON(%s): %v", in, err)
			}
			if len(got) != 0 {
				t.Errorf("ManualURLsFromJSON(%s) = %v, want none", in, got)
			}
		}
	})
}

// TestDLCNamesFromJSON locks the /downloads/ extraction and the first-wins
// de-duplication.
func TestDLCNamesFromJSON(t *testing.T) {
	cases := []struct {
		name string
		in   jsontext.Value
		want []string
	}{
		{
			name: "names between /downloads/ and the last slash",
			in: jsontext.Value(`[
				{"manualUrl":"https://www.gog.com/downloads/dlc_one/setup.exe"},
				{"manualUrl":"https://www.gog.com/downloads/dlc_two/patch.exe"}
			]`),
			want: []string{"dlc_one", "dlc_two"},
		},
		{
			name: "first occurrence wins",
			in: jsontext.Value(`[
				{"manualUrl":"https://x/downloads/dup/a"},
				{"manualUrl":"https://x/downloads/other/a"},
				{"manualUrl":"https://x/downloads/dup/b"}
			]`),
			want: []string{"dup", "other"},
		},
		{
			name: "urls without the marker are skipped",
			in: jsontext.Value(`[
				{"manualUrl":"https://x/other/thing"},
				{"manualUrl":"https://x/downloads/kept/f"}
			]`),
			want: []string{"kept"},
		},
		{
			name: "marker without a following slash is skipped",
			in:   jsontext.Value(`[{"manualUrl":"https://x/downloads/"}]`),
			want: nil,
		},
		{name: "empty input", in: nil, want: nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := DLCNamesFromJSON(c.in)
			if err != nil {
				t.Fatalf("DLCNamesFromJSON: %v", err)
			}
			if len(got) != len(c.want) {
				t.Fatalf("names = %v, want %v", got, c.want)
			}
			for i := range c.want {
				if got[i] != c.want[i] {
					t.Errorf("names[%d] = %q, want %q", i, got[i], c.want[i])
				}
			}
		})
	}
}
