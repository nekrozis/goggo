package util

import "testing"

// TestManualURLsFromJSON locks the collection order: arrays keep their element
// order, object members are visited in sorted key order so the result stays
// deterministic even though Go's map does not preserve document order.
func TestManualURLsFromJSON(t *testing.T) {
	t.Run("array keeps order", func(t *testing.T) {
		in := []any{
			map[string]any{"manualUrl": "b"},
			map[string]any{"manualUrl": "a"},
		}
		got, err := ManualURLsFromJSON(in)
		if err != nil {
			t.Fatalf("ManualURLsFromJSON: %v", err)
		}
		if len(got) != 2 || got[0] != "b" || got[1] != "a" {
			t.Errorf("urls = %v, want [b a]", got)
		}
	})

	t.Run("object keys are sorted", func(t *testing.T) {
		in := map[string]any{
			"2": map[string]any{"manualUrl": "second"},
			"1": map[string]any{"manualUrl": "first"},
			"3": map[string]any{"manualUrl": "third"},
		}
		got, err := ManualURLsFromJSON(in)
		if err != nil {
			t.Fatalf("ManualURLsFromJSON: %v", err)
		}
		if len(got) != 3 || got[0] != "first" || got[1] != "second" || got[2] != "third" {
			t.Errorf("urls = %v, want key order 1,2,3", got)
		}
	})

	t.Run("nested containers are walked", func(t *testing.T) {
		in := map[string]any{
			"outer": []any{
				map[string]any{"inner": map[string]any{"manualUrl": "deep"}},
			},
		}
		got, err := ManualURLsFromJSON(in)
		if err != nil {
			t.Fatalf("ManualURLsFromJSON: %v", err)
		}
		if len(got) != 1 || got[0] != "deep" {
			t.Errorf("urls = %v", got)
		}
	})

	t.Run("a manualUrl member is not recursed into", func(t *testing.T) {
		in := map[string]any{"manualUrl": map[string]any{"manualUrl": "hidden"}}
		if _, err := ManualURLsFromJSON(in); err == nil {
			t.Error("want error for a non-scalar manualUrl")
		}
	})

	t.Run("scalars and empty input contribute nothing", func(t *testing.T) {
		for _, in := range []any{nil, "text", float64(1), []any{}, map[string]any{}} {
			got, err := ManualURLsFromJSON(in)
			if err != nil {
				t.Fatalf("ManualURLsFromJSON(%#v): %v", in, err)
			}
			if len(got) != 0 {
				t.Errorf("ManualURLsFromJSON(%#v) = %v, want none", in, got)
			}
		}
	})
}

// TestDLCNamesFromJSON locks the /downloads/ extraction and the first-wins
// de-duplication.
func TestDLCNamesFromJSON(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want []string
	}{
		{
			name: "names between /downloads/ and the last slash",
			in: []any{
				map[string]any{"manualUrl": "https://www.gog.com/downloads/dlc_one/setup.exe"},
				map[string]any{"manualUrl": "https://www.gog.com/downloads/dlc_two/patch.exe"},
			},
			want: []string{"dlc_one", "dlc_two"},
		},
		{
			name: "first occurrence wins",
			in: []any{
				map[string]any{"manualUrl": "https://x/downloads/dup/a"},
				map[string]any{"manualUrl": "https://x/downloads/other/a"},
				map[string]any{"manualUrl": "https://x/downloads/dup/b"},
			},
			want: []string{"dup", "other"},
		},
		{
			name: "urls without the marker are skipped",
			in: []any{
				map[string]any{"manualUrl": "https://x/other/thing"},
				map[string]any{"manualUrl": "https://x/downloads/kept/f"},
			},
			want: []string{"kept"},
		},
		{
			name: "marker without a following slash is skipped",
			in:   []any{map[string]any{"manualUrl": "https://x/downloads/"}},
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
