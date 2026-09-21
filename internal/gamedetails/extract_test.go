package gamedetails

import (
	"strings"
	"testing"
)

// The two extractions extract.go performs on a per-game details document: the
// serials text of the cdKey member and the standalone changelog document. Both
// moved here from internal/core, which used to lock another package's output
// shapes from the outside.

// TestSerialsFromCDKeyShapes locks the cdKey shapes: the break markup becomes a
// line break, the match is case-sensitive, and a <span> cdKey is reported as
// unsupported rather than half-normalized.
func TestSerialsFromCDKeyShapes(t *testing.T) {
	cases := []struct {
		in, want    string
		unsupported bool
	}{
		{"ABC-123", "ABC-123\n", false},
		{"a<br>b", "a\nb\n", false},
		{"a<br/>b<br />c", "a\nb\nc\n", false},
		{"", "", false},
		{"<span>x</span>", "", true},
		{"<BR>", "<BR>\n", false}, // the regex is case-sensitive: not a break
	}
	for _, tc := range cases {
		got, unsupported := SerialsFromCDKey(tc.in)
		if got != tc.want || unsupported != tc.unsupported {
			t.Errorf("SerialsFromCDKey(%q) = %q/%v, want %q/%v", tc.in, got, unsupported, tc.want, tc.unsupported)
		}
	}
}

// TestChangelogFromJSONWrapping locks the wrapper format and the title rule:
// the document is standalone HTML, the title is presence-based so an empty one
// still renders, and a member of the wrong shape is an error rather than a
// coercion.
func TestChangelogFromJSONWrapping(t *testing.T) {
	got, err := ChangelogFromJSON([]byte(`{"changelog": "<p>fix</p>", "title": "Game"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, `<!DOCTYPE html>`) || !strings.Contains(got, "<title>Changelog: Game</title>") ||
		!strings.HasSuffix(got, "<body><p>fix</p></body>\n</html>") { // the literal has the newline
		t.Errorf("wrapped changelog = %q", got)
	}
	if got, _ := ChangelogFromJSON([]byte(`{"changelog": ""}`)); got != "" {
		t.Errorf("empty changelog = %q, want nothing", got)
	}
	if got, _ := ChangelogFromJSON([]byte(`{}`)); got != "" {
		t.Errorf("missing changelog = %q, want nothing", got)
	}
	// The title test is presence, not value: an empty or null
	// title still renders "Changelog: ".
	got, _ = ChangelogFromJSON([]byte(`{"changelog": "c", "title": ""}`))
	if !strings.Contains(got, "<title>Changelog: </title>") {
		t.Errorf("present-empty title = %q, want the trailing-space form", got)
	}
	got, err = ChangelogFromJSON([]byte(`{"changelog": "c", "title": null}`))
	if err != nil {
		t.Fatalf("null title unexpected error: %v", err)
	}
	if !strings.Contains(got, "<title>Changelog: </title>") {
		t.Errorf("null title = %q, want the trailing-space form", got)
	}

	// The shape rule: a member that has no string form is an error, never a
	// coerced rendering.
	if _, err := ChangelogFromJSON([]byte(`{"changelog": ["x"]}`)); err == nil {
		t.Error("a structured changelog must be an error")
	}
	if _, err := ChangelogFromJSON([]byte(`{"changelog": "c", "title": {}}`)); err == nil {
		t.Error("a structured title must be an error")
	}
}
