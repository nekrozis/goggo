package util

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// v1StyledReference renders v with the standard library's original JSON encoder.
// It is an independent oracle for the styled shape: a separate implementation, so
// agreement with the seam is evidence rather than a tautology.
func v1StyledReference(t *testing.T, v any) string {
	t.Helper()
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetIndent("", "\t")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		t.Fatalf("v1 reference: %v", err)
	}
	return b.String()
}

// TestWriteStyledJSONStyleContract locks the styled shape.
//
// Contract (format): tab indentation, keys in byte order, no HTML escaping (a
// url's "&" and "<" must survive), and a trailing newline.
func TestWriteStyledJSONStyleContract(t *testing.T) {
	var buf bytes.Buffer
	doc := map[string]any{
		"z": "https://cdn.gog.com/x?a=1&b=<2>",
		"a": map[string]any{"items": []any{float64(2), true}},
	}
	if err := WriteStyledJSON(&buf, doc); err != nil {
		t.Fatalf("WriteStyledJSON: %v", err)
	}
	want := "{\n" +
		"\t\"a\": {\n" +
		"\t\t\"items\": [\n" +
		"\t\t\t2,\n" +
		"\t\t\ttrue\n" +
		"\t\t]\n" +
		"\t},\n" +
		"\t\"z\": \"https://cdn.gog.com/x?a=1&b=<2>\"\n" +
		"}\n"
	if buf.String() != want {
		t.Errorf("got %q\nwant %q", buf.String(), want)
	}
}

// TestStyledJSONTrimsTrailingNewline locks the stored form.
//
// Contract (format): the same text as WriteStyledJSON, without its trailing newline.
func TestStyledJSONTrimsTrailingNewline(t *testing.T) {
	got, err := StyledJSON(map[string]any{"k": "v"})
	if err != nil {
		t.Fatalf("StyledJSON: %v", err)
	}
	if strings.HasSuffix(got, "\n") {
		t.Errorf("StyledJSON keeps the writer's trailing newline: %q", got)
	}
	if got != "{\n\t\"k\": \"v\"\n}" {
		t.Errorf("got %q, want the tab-indented object", got)
	}
}

// TestStyledJSONDeterministicMapOrdering locks what the deterministic option
// buys: the same Go value renders to the same bytes on every call, and a Go
// map's members come out in sorted key order.
//
// Contract (format): map members in sorted key order.
//
// The members are inserted in an order that is deliberately NOT sorted, so a
// missing deterministic option cannot pass by walking the map into the sorted
// order by accident: without it the encoder emits a rotation of the insertion
// order, which is never the sorted order for this input. Asserting the exact
// bytes therefore kills that mutation outright, where merely comparing repeated
// runs against each other would only catch it by luck.
func TestStyledJSONDeterministicMapOrdering(t *testing.T) {
	doc := map[string]any{}
	for _, key := range []string{"m", "c", "z", "a", "q", "b", "y", "d", "k", "w"} {
		doc[key] = float64(1)
	}
	want := "{\n" +
		"\t\"a\": 1,\n" +
		"\t\"b\": 1,\n" +
		"\t\"c\": 1,\n" +
		"\t\"d\": 1,\n" +
		"\t\"k\": 1,\n" +
		"\t\"m\": 1,\n" +
		"\t\"q\": 1,\n" +
		"\t\"w\": 1,\n" +
		"\t\"y\": 1,\n" +
		"\t\"z\": 1\n" +
		"}"
	for i := 1; i <= 5; i++ {
		got, err := StyledJSON(doc)
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		if got != want {
			t.Fatalf("run %d: got %s, want the members in sorted key order", i, got)
		}
	}
}

// TestStyledJSONNilCollectionSemantics locks the collection shapes a document
// written through this seam carries.
//
// Contract (format): a nil slice is [] and a nil map is {}, not null.
func TestStyledJSONNilCollectionSemantics(t *testing.T) {
	doc := struct {
		Items []int
		Table map[string]int
	}{}
	got, err := StyledJSON(doc)
	if err != nil {
		t.Fatalf("StyledJSON: %v", err)
	}
	if want := "{\n\t\"Items\": [],\n\t\"Table\": {}\n}"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestStyledJSONFloatFormattingMatchesV1 pins how a number is spelled.
//
// Contract (format): a float is spelled exactly as the original encoder spelled it.
func TestStyledJSONFloatFormattingMatchesV1(t *testing.T) {
	doc := map[string]any{
		"whole":    float64(1700000000),
		"big":      float64(1207659156),
		"frac":     float64(1.5),
		"tiny":     float64(1e-7),
		"wide":     float64(58812465975493914),
		"negative": float64(-3),
	}
	want := v1StyledReference(t, doc)
	got, err := StyledJSON(doc)
	if err != nil {
		t.Fatalf("StyledJSON: %v", err)
	}
	if got+"\n" != want {
		t.Errorf("float rendering changed:\n got %s\nwant %s", got, strings.TrimRight(want, "\n"))
	}
}

// TestStyledJSONGameDetailsGolden locks the rendering of the game details document
// — the shape webapi.GameDetailsJSON hands to core/gameinfo.go, written to disk as
// the game-details.json artifact.
//
// Contract (format): the artifact's bytes, against an independent encoder.
func TestStyledJSONGameDetailsGolden(t *testing.T) {
	details := map[string]any{
		"gamename": "the_witcher_3_wild_hunt",
		"id":       float64(1207659156),
		"title":    "The Witcher 3: Wild Hunt",
		"cdKey":    "ABCD-EFGH-IJKL-MNOP",
		"images": map[string]any{
			"icon": "//images.gog.com/icon.jpg",
			"logo": "//images.gog.com/logo_glx_logo.jpg",
		},
		"downloads": map[string]any{
			"installers": []any{map[string]any{
				"name":     "setup_the_witcher_3",
				"version":  "4.0",
				"language": "en",
				"files":    []any{map[string]any{"id": "en1installer0", "downlink": "https://cdn.gog.com/dl/installer", "size": "1495134320"}},
			}},
		},
	}
	want := v1StyledReference(t, details)
	got, err := StyledJSON(details)
	if err != nil {
		t.Fatalf("StyledJSON: %v", err)
	}
	if got+"\n" != want {
		t.Errorf("game-details rendering changed:\n got %s\nwant %s", got, strings.TrimRight(want, "\n"))
	}
}

// TestStyledJSONBytesPreservesRaw locks what the raw path is for.
//
// Contract (format): member order and number literals come back exactly as they were
// encoded; only the whitespace is regenerated.
func TestStyledJSONBytesPreservesRaw(t *testing.T) {
	raw := `{"zeta":58812465975493914,"alpha":1.5,"nested":{"b":1e2,"a":0.0},"flag":true,"tail":-0}`
	want := "{\n" +
		"\t\"zeta\": 58812465975493914,\n" +
		"\t\"alpha\": 1.5,\n" +
		"\t\"nested\": {\n" +
		"\t\t\"b\": 1e2,\n" +
		"\t\t\"a\": 0.0\n" +
		"\t},\n" +
		"\t\"flag\": true,\n" +
		"\t\"tail\": -0\n" +
		"}\n"
	var buf bytes.Buffer
	if err := WriteStyledJSONBytes(&buf, []byte(raw)); err != nil {
		t.Fatalf("WriteStyledJSONBytes: %v", err)
	}
	if buf.String() != want {
		t.Errorf("got %q\nwant %q", buf.String(), want)
	}
}

// TestStyledJSONBytesTopLevelShapes covers the scalar top-level values the artifact
// writers can meet, not only objects.
//
// Contract (format): each top-level shape's exact rendering.
func TestStyledJSONBytesTopLevelShapes(t *testing.T) {
	for raw, want := range map[string]string{
		`[1,2,3]`:  "[\n\t1,\n\t2,\n\t3\n]\n",
		`  null  `: "null\n",
		`42`:       "42\n",
		`"text"`:   "\"text\"\n",
	} {
		var buf bytes.Buffer
		if err := WriteStyledJSONBytes(&buf, []byte(raw)); err != nil {
			t.Fatalf("WriteStyledJSONBytes(%s): %v", raw, err)
		}
		if buf.String() != want {
			t.Errorf("WriteStyledJSONBytes(%s) = %q, want %q", raw, buf.String(), want)
		}
	}
}

// TestStyledJSONBytesRejectsInput locks the raw path's input boundary. It is a
// validator as well as a formatter, with the same rules the decoders enforce.
func TestStyledJSONBytesRejectsInput(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"empty", ""},
		{"whitespace only", "   \n\t"},
		{"malformed", "{not json"},
		{"truncated", `{"a":1`},
		{"second top-level value", `{"a":1}{"b":2}`},
		{"trailing garbage", `{"a":1} oops`},
		{"duplicate member", `{"a":1,"a":2}`},
		{"invalid utf-8", "{\"a\":\"\xff\xfe\"}"},

		// Whitespace that is not one of JSON's four characters must not be
		// trimmed away as if it were: each of these is a document the boundary
		// has to refuse. The multi-byte characters are built from their bytes
		// rather than written literally, so a tool that resolves escapes on the
		// way in cannot silently change what is being tested.
		{"no-break space prefix", string([]byte{0xc2, 0xa0}) + `{"a":1}`},
		{"no-break space suffix", `{"a":1}` + string([]byte{0xc2, 0xa0})},
		{"no-break space only", string([]byte{0xc2, 0xa0})},
		{"ideographic space prefix", string([]byte{0xe3, 0x80, 0x80}) + `{"a":1}`},
		{"vertical tab prefix", string([]byte{0x0b}) + `{"a":1}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := WriteStyledJSONBytes(&buf, []byte(tc.raw)); err == nil {
				t.Errorf("WriteStyledJSONBytes(%q) = %q, want an error", tc.raw, buf.String())
			}
		})
	}
}

// TestStyledJSONBytesPreservesEscapeSpelling locks the PreserveRawStrings half of
// the raw contract.
//
// Contract (format): a string keeps the escape spelling it was written with, so the
// document is reformatted rather than re-encoded.
//
// The backslash is built from its byte value rather than written literally, so a tool
// that resolves escapes on the way in cannot silently flatten the fixture.
func TestStyledJSONBytesPreservesEscapeSpelling(t *testing.T) {
	backslash := string([]byte{92})
	raw := `{"lit":"` + backslash + `u0041","nl":"a` + backslash + `nb"}`

	var buf bytes.Buffer
	if err := WriteStyledJSONBytes(&buf, []byte(raw)); err != nil {
		t.Fatalf("WriteStyledJSONBytes: %v", err)
	}
	want := "{\n" +
		"\t\"lit\": \"" + backslash + "u0041\",\n" +
		"\t\"nl\": \"a" + backslash + "nb\"\n" +
		"}\n"
	if buf.String() != want {
		t.Errorf("got %q\nwant %q", buf.String(), want)
	}
}

// TestStyledJSONBytesTrailingNewline locks the writer/stored split on the raw path,
// matching the typed path.
//
// Contract (format): the writer emits a trailing newline; the stored form does not.
func TestStyledJSONBytesTrailingNewline(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteStyledJSONBytes(&buf, []byte(`{"a":1}`)); err != nil {
		t.Fatalf("WriteStyledJSONBytes: %v", err)
	}
	if !strings.HasSuffix(buf.String(), "\n") {
		t.Errorf("WriteStyledJSONBytes drops the trailing newline: %q", buf.String())
	}
	got, err := StyledJSONBytes([]byte(`{"a":1}`))
	if err != nil {
		t.Fatalf("StyledJSONBytes: %v", err)
	}
	if strings.HasSuffix(got, "\n") {
		t.Errorf("StyledJSONBytes keeps the trailing newline: %q", got)
	}
}

// TestStyledJSONBytesToleratesSurroundingWhitespace: whitespace around the single
// top-level value is not content.
//
// Contract (format): the surrounding whitespace is dropped, the value is styled.
func TestStyledJSONBytesToleratesSurroundingWhitespace(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteStyledJSONBytes(&buf, []byte("  \n\t{\"a\":1}\n  ")); err != nil {
		t.Fatalf("WriteStyledJSONBytes: %v", err)
	}
	if want := "{\n\t\"a\": 1\n}\n"; buf.String() != want {
		t.Errorf("got %q, want %q", buf.String(), want)
	}
}
