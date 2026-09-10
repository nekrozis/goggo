package galaxy

import (
	"encoding/json"
	"strings"
	"testing"
)

// linkDoc decodes a literal link document.
func linkDoc(t *testing.T, raw string) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatalf("unmarshal %s: %v", raw, err)
	}
	return v
}

// TestGalaxyPathPlaceholder locks the marker's exact value: the download layer
// substitutes this string, so it is a contract between the two. The marker is
// goggo's own template protocol — the API never sends it — and it deliberately
// reuses the "{path}" spelling the API's url_format already uses for the path
// parameter, whose value the parameter pass re-attaches the marker to.
func TestGalaxyPathPlaceholder(t *testing.T) {
	if GalaxyPathPlaceholder != "{path}" {
		t.Errorf("GalaxyPathPlaceholder = %q, want %q", GalaxyPathPlaceholder, "{path}")
	}
}

// TestCdnURLTemplatesRanking locks galaxyapi.cpp:750-767 and the ordering of the
// result: configured endpoints first, in priority order, then the unlisted ones.
func TestCdnURLTemplatesRanking(t *testing.T) {
	doc := linkDoc(t, `{"urls":[`+
		`{"endpoint_name":"cdnC","url_format":"C"},`+
		`{"endpoint_name":"cdnB","url_format":"B"},`+
		`{"endpoint_name":"cdnA","url_format":"A"}]}`)

	got, err := CdnURLTemplatesFromJSON(doc, []string{"cdnA", "cdnB"})
	if err != nil {
		t.Fatalf("CdnURLTemplatesFromJSON: %v", err)
	}
	want := []string{"A", "B", "C"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("templates = %v, want %v", got, want)
	}
}

// TestCdnURLTemplatesUnknownDocumentOrder: with nothing in the priority list
// every endpoint is ranked by its index, so document order survives.
func TestCdnURLTemplatesUnknownDocumentOrder(t *testing.T) {
	doc := linkDoc(t, `{"urls":[{"endpoint_name":"x","url_format":"1"},{"endpoint_name":"y","url_format":"2"}]}`)

	got, err := CdnURLTemplatesFromJSON(doc, nil)
	if err != nil {
		t.Fatalf("CdnURLTemplatesFromJSON: %v", err)
	}
	if len(got) != 2 || got[0] != "1" || got[1] != "2" {
		t.Errorf("templates = %v, want [1 2]", got)
	}
}

// TestCdnURLTemplatesStableForDuplicateNames: two entries for the same endpoint
// get the same score, and the stable sort keeps document order — the case
// std::sort leaves undefined.
func TestCdnURLTemplatesStableForDuplicateNames(t *testing.T) {
	doc := linkDoc(t, `{"urls":[`+
		`{"endpoint_name":"cdn","url_format":"first"},`+
		`{"endpoint_name":"other","url_format":"unknown"},`+
		`{"endpoint_name":"cdn","url_format":"second"}]}`)

	got, err := CdnURLTemplatesFromJSON(doc, []string{"cdn"})
	if err != nil {
		t.Fatalf("CdnURLTemplatesFromJSON: %v", err)
	}
	want := []string{"first", "second", "unknown"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("templates = %v, want %v", got, want)
	}
}

// TestCdnURLTemplatesPlaceholders locks the replacement rule: every occurrence is
// replaced, and the value of {path} gets the marker APPENDED rather than being
// replaced by it (galaxyapi.cpp:781-784). No normalisation happens, so the double
// slash stays.
func TestCdnURLTemplatesPlaceholders(t *testing.T) {
	doc := linkDoc(t, `{"urls":[{"endpoint_name":"cdn",`+
		`"url_format":"https://x/{path}?a={alpha}&b={alpha}",`+
		`"parameters":{"path":"/p","alpha":"A"}}]}`)

	got, err := CdnURLTemplatesFromJSON(doc, nil)
	if err != nil {
		t.Fatalf("CdnURLTemplatesFromJSON: %v", err)
	}
	want := "https://x//p" + GalaxyPathPlaceholder + "?a=A&b=A"
	if len(got) != 1 || got[0] != want {
		t.Errorf("templates = %v, want [%s]", got, want)
	}
}

// TestCdnURLTemplatesKeyOrder locks the ascending-key-order rule, which is
// observable: {a} expands to "{b}", and that text is then replaced as well.
// Reversing the order would leave "{b}" in the result.
func TestCdnURLTemplatesKeyOrder(t *testing.T) {
	doc := linkDoc(t, `{"urls":[{"endpoint_name":"cdn","url_format":"x{a}y{b}",`+
		`"parameters":{"b":"B","a":"{b}"}}]}`)

	got, err := CdnURLTemplatesFromJSON(doc, nil)
	if err != nil {
		t.Fatalf("CdnURLTemplatesFromJSON: %v", err)
	}
	if len(got) != 1 || got[0] != "xByB" {
		t.Errorf("templates = %v, want [xByB]", got)
	}
}

// TestCdnURLTemplatesShape locks the boundary: a missing or null scalar is "",
// an absent or null section is no templates, and a section that is present in the
// wrong shape is reported.
func TestCdnURLTemplatesShape(t *testing.T) {
	cases := []struct {
		name    string
		doc     string
		want    []string
		wantErr bool
	}{
		{name: "no urls", doc: `{}`, want: nil},
		{name: "null urls", doc: `{"urls":null}`, want: nil},
		{name: "empty urls", doc: `{"urls":[]}`, want: []string{}},
		{name: "string urls", doc: `{"urls":"nope"}`, wantErr: true},
		{name: "non-object entry", doc: `{"urls":[5]}`, wantErr: true},
		{name: "string parameters", doc: `{"urls":[{"parameters":"nope"}]}`, wantErr: true},
		{name: "object url_format", doc: `{"urls":[{"url_format":{}}]}`, wantErr: true},
		{name: "non-scalar parameter", doc: `{"urls":[{"parameters":{"a":{}}}]}`, wantErr: true},
		{name: "null parameters equals missing", doc: `{"urls":[{"url_format":"abc","parameters":null}]}`, want: []string{"abc"}},
		{name: "missing url_format kept as empty", doc: `{"urls":[{"endpoint_name":"cdn"}]}`, want: []string{""}},
		{name: "endpoint_name coerces", doc: `{"urls":[{"endpoint_name":5,"url_format":"ok"}]}`, want: []string{"ok"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := CdnURLTemplatesFromJSON(linkDoc(t, c.doc), []string{"cdnA", "cdnB"})
			if c.wantErr {
				if err == nil {
					t.Fatal("the document must be reported as broken")
				}
				return
			}
			if err != nil {
				t.Fatalf("CdnURLTemplatesFromJSON: %v", err)
			}
			if strings.Join(got, "|") != strings.Join(c.want, "|") {
				t.Errorf("templates = %v, want %v", got, c.want)
			}
		})
	}
}

// TestCdnURLTemplatesEmptyFormatKept: an empty template stays in the list. Whether
// a URL is usable is the caller's decision, not this layer's (review ruling D12).
func TestCdnURLTemplatesEmptyFormatKept(t *testing.T) {
	doc := linkDoc(t, `{"urls":[{"url_format":""},{"endpoint_name":"cdn","url_format":"u"}]}`)

	got, err := CdnURLTemplatesFromJSON(doc, []string{"cdn"})
	if err != nil {
		t.Fatalf("CdnURLTemplatesFromJSON: %v", err)
	}
	// "u" is ranked first (it is the listed endpoint), the empty template second.
	if len(got) != 2 || got[0] != "u" || got[1] != "" {
		t.Errorf("templates = %v, want [u ]", got)
	}
}

// TestCdnURLTemplatesCoercion: a numeric parameter value stringifies the way
// jsoncpp's asString does.
func TestCdnURLTemplatesCoercion(t *testing.T) {
	doc := linkDoc(t, `{"urls":[{"url_format":"x{path}","parameters":{"path":5}}]}`)

	got, err := CdnURLTemplatesFromJSON(doc, nil)
	if err != nil {
		t.Fatalf("CdnURLTemplatesFromJSON: %v", err)
	}
	want := "x5" + GalaxyPathPlaceholder
	if len(got) != 1 || got[0] != want {
		t.Errorf("templates = %v, want [%s]", got, want)
	}
}

// TestPathFromDownlinkURL locks the derivation of galaxyapi.cpp:676-739.
func TestPathFromDownlinkURL(t *testing.T) {
	cases := []struct {
		name  string
		url   string
		game  string
		want  string
		notes string
	}{
		{
			name: "percent decoding", url: "/dl/game/File%20Name.bin", game: "game",
			want: "/game/File Name.bin",
		},
		{
			name: "+ is not a space", url: "/dl/game/a+b", game: "game",
			want: "/game/a+b",
		},
		{
			name: "gamename segment wins", url: "/x/game/sub/file.bin", game: "game",
			want: "/game/sub/file.bin",
		},
		{
			name: "no gamename segment", url: "/x/y/file.bin", game: "game",
			want: "/game/file.bin",
		},
		{
			name: "query is cut", url: "/game/dir/file?token=X", game: "game",
			want: "/game/dir/file",
		},
		{
			name: "second trailing slash stays", url: "/game/sub//", game: "game",
			want:  "/game/sub/",
			notes: "exactly one slash is removed",
		},
		{
			name: "invalid escape keeps the text", url: "/game/a%zzb", game: "game",
			want: "/game/a%zzb",
		},
		{
			name: "no slash at all", url: "file.bin", game: "game",
			want: "/game/file.bin",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := PathFromDownlinkURL(c.url, c.game); got != c.want {
				t.Errorf("PathFromDownlinkURL(%q, %q) = %q, want %q (%s)", c.url, c.game, got, c.want, c.notes)
			}
		})
	}
}

// TestPathFromDownlinkURLIssue126 locks the workaround of issue #126: a "?" that
// follows the last "/" means the format was unexpected, so the path is cut there.
func TestPathFromDownlinkURLIssue126(t *testing.T) {
	cases := []struct {
		name string
		url  string
		want string
	}{
		{
			// Both markers present: the cut happens at the earlier of the two,
			// which leaves "?path=x" behind — and the workaround then removes it
			// because that "?" follows the last "/".
			name: "both token markers", url: "/game/f?path=x&token=T&access_token=A",
			want: "/game/f",
		},
		{
			name: "one token marker", url: "/game/f?path=x&token=T",
			want: "/game/f",
		},
		{
			name: "stray query is cut", url: "?x",
			want: "/game/",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := PathFromDownlinkURL(c.url, "game"); got != c.want {
				t.Errorf("PathFromDownlinkURL(%q, %q) = %q, want %q", c.url, "game", got, c.want)
			}
		})
	}
}

// TestPathFromDownlinkURLEmpty locks the defined behaviour for an empty URL,
// where the C++ source would read past the end of the string.
func TestPathFromDownlinkURLEmpty(t *testing.T) {
	for _, input := range []string{"", "/"} {
		if got := PathFromDownlinkURL(input, "game"); got != "/game/" {
			t.Errorf("PathFromDownlinkURL(%q, %q) = %q, want %q", input, "game", got, "/game/")
		}
	}
}
