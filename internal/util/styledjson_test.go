package util

import (
	"bytes"
	"strings"
	"testing"
)

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

// TestStyledJSONDeterministicMapOrdering locks what the deterministic option
// buys: the same Go value renders to the same bytes on every call, and a Go
// map's members come out in sorted key order.
//
// Contract (format): map members in sorted key order.
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
		"}\n"
	for i := 1; i <= 5; i++ {
		var buf bytes.Buffer
		if err := WriteStyledJSON(&buf, doc); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		if got := buf.String(); got != want {
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
	var buf bytes.Buffer
	if err := WriteStyledJSON(&buf, doc); err != nil {
		t.Fatalf("WriteStyledJSON: %v", err)
	}
	if want := "{\n\t\"Items\": [],\n\t\"Table\": {}\n}\n"; buf.String() != want {
		t.Errorf("got %q, want %q", buf.String(), want)
	}
}

// TestWriteStyledJSONFloatFormattingGolden pins how a number is spelled.
//
// Contract (format): a float is spelled exactly as the styled encoder spells it.
func TestWriteStyledJSONFloatFormattingGolden(t *testing.T) {
	doc := map[string]any{
		"whole":    float64(1700000000),
		"big":      float64(1207659156),
		"frac":     float64(1.5),
		"tiny":     float64(1e-7),
		"wide":     float64(58812465975493914),
		"negative": float64(-3),
	}
	var buf bytes.Buffer
	if err := WriteStyledJSON(&buf, doc); err != nil {
		t.Fatalf("WriteStyledJSON: %v", err)
	}
	want := "{\n" +
		"\t\"big\": 1207659156,\n" +
		"\t\"frac\": 1.5,\n" +
		"\t\"negative\": -3,\n" +
		"\t\"tiny\": 1e-7,\n" +
		"\t\"whole\": 1700000000,\n" +
		"\t\"wide\": 58812465975493910\n" +
		"}\n"
	if got := buf.String(); got != want {
		t.Errorf("float rendering changed:\n got %s\nwant %s", got, want)
	}
}

// TestWriteStyledJSONGameDetailsGolden locks the rendering of the game details document
// — the shape webapi.GameDetailsJSON hands to core/gameinfo.go, written to disk as
// the game-details.json artifact.
//
// Contract (format): the artifact's bytes, against frozen golden output.
func TestWriteStyledJSONGameDetailsGolden(t *testing.T) {
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
	var buf bytes.Buffer
	if err := WriteStyledJSON(&buf, details); err != nil {
		t.Fatalf("WriteStyledJSON: %v", err)
	}
	want := "{\n" +
		"\t\"cdKey\": \"ABCD-EFGH-IJKL-MNOP\",\n" +
		"\t\"downloads\": {\n" +
		"\t\t\"installers\": [\n" +
		"\t\t\t{\n" +
		"\t\t\t\t\"files\": [\n" +
		"\t\t\t\t\t{\n" +
		"\t\t\t\t\t\t\"downlink\": \"https://cdn.gog.com/dl/installer\",\n" +
		"\t\t\t\t\t\t\"id\": \"en1installer0\",\n" +
		"\t\t\t\t\t\t\"size\": \"1495134320\"\n" +
		"\t\t\t\t\t}\n" +
		"\t\t\t\t],\n" +
		"\t\t\t\t\"language\": \"en\",\n" +
		"\t\t\t\t\"name\": \"setup_the_witcher_3\",\n" +
		"\t\t\t\t\"version\": \"4.0\"\n" +
		"\t\t\t}\n" +
		"\t\t]\n" +
		"\t},\n" +
		"\t\"gamename\": \"the_witcher_3_wild_hunt\",\n" +
		"\t\"id\": 1207659156,\n" +
		"\t\"images\": {\n" +
		"\t\t\"icon\": \"//images.gog.com/icon.jpg\",\n" +
		"\t\t\"logo\": \"//images.gog.com/logo_glx_logo.jpg\"\n" +
		"\t},\n" +
		"\t\"title\": \"The Witcher 3: Wild Hunt\"\n" +
		"}\n"
	if got := buf.String(); got != want {
		t.Errorf("game-details rendering changed:\n got %s\nwant %s", got, want)
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

		// Non-JSON whitespace characters must be refused by the boundary.
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
