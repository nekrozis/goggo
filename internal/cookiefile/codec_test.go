package cookiefile

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func pc(domain, path, name, value string, hostOnly bool) PersistentCookie {
	return PersistentCookie{
		Domain:   domain,
		Path:     path,
		Name:     name,
		Value:    value,
		HostOnly: hostOnly,
	}
}

func TestRoundTripBasic(t *testing.T) {
	in := []PersistentCookie{
		pc(".example.com", "/", "sid", "abc123", false),
		pc("example.com", "/foo", "host", "v", true),
		pc(".secure.test", "/", "tok", "x", false),
	}
	in[2].Secure = true
	exp := time.Unix(1700000000, 0)
	in[2].Expires = exp

	var buf bytes.Buffer
	n, err := Write(&buf, in)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != len(in) {
		t.Errorf("written = %d, want %d", n, len(in))
	}

	out := Parse(buf.Bytes())
	if len(out) != len(in) {
		t.Fatalf("parsed %d rows, want %d\n%s", len(out), len(in), buf.String())
	}
	for i := range in {
		checkCookie(t, i, in[i], out[i])
	}
}

func checkCookie(t *testing.T, i int, want, got PersistentCookie) {
	t.Helper()
	if got.Domain != want.Domain {
		t.Errorf("row %d: domain = %q, want %q", i, got.Domain, want.Domain)
	}
	if got.Path != want.Path {
		t.Errorf("row %d: path = %q, want %q", i, got.Path, want.Path)
	}
	if got.Name != want.Name {
		t.Errorf("row %d: name = %q, want %q", i, got.Name, want.Name)
	}
	if got.Value != want.Value {
		t.Errorf("row %d: value = %q, want %q", i, got.Value, want.Value)
	}
	if got.Secure != want.Secure {
		t.Errorf("row %d: secure = %v, want %v", i, got.Secure, want.Secure)
	}
	if got.HttpOnly != want.HttpOnly {
		t.Errorf("row %d: httpOnly = %v, want %v", i, got.HttpOnly, want.HttpOnly)
	}
	if got.HostOnly != want.HostOnly {
		t.Errorf("row %d: hostOnly = %v, want %v", i, got.HostOnly, want.HostOnly)
	}
	if !got.Expires.Equal(want.Expires) {
		t.Errorf("row %d: expires = %v, want %v", i, got.Expires, want.Expires)
	}
}

func TestHttpOnlyRoundTrip(t *testing.T) {
	// The curl extension marks HttpOnly rows via the domain prefix.
	input := "#HttpOnly_.gog.com\tTRUE\t/\tTRUE\t0\tsid\tabc\n"
	out := Parse([]byte(input))
	if len(out) != 1 {
		t.Fatalf("parsed %d rows, want 1", len(out))
	}
	c := out[0]
	if c.Domain != ".gog.com" || !c.HttpOnly || c.HostOnly {
		t.Errorf("parsed = %+v, want domain .gog.com httpOnly hostOnly=false", c)
	}

	var buf bytes.Buffer
	if _, err := Write(&buf, []PersistentCookie{c}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(buf.String(), headerLine+"\n") {
		t.Errorf("missing header line: %q", buf.String())
	}
	// Row line must still carry the prefix after re-encoding.
	lines := strings.Split(buf.String(), "\n")
	if !strings.HasPrefix(lines[1], "#HttpOnly_.gog.com\t") {
		t.Errorf("row = %q, want #HttpOnly_ prefix", lines[1])
	}
}

func TestCommentsBlankLinesAndGarbageSkipped(t *testing.T) {
	input := "# Netscape HTTP Cookie File\n" +
		"# This is a comment\n" +
		"\n" +
		"example.com\tFALSE\t/\tFALSE\t0\tn1\tv1\n" +
		"#HttpOnly_.x.com\tTRUE\t/\tTRUE\t100\tn2\tv2\n" +
		"too\tfew\tcols\n" +
		"a\tb\tc\td\te\tf\tg\th\n" + // 8 columns
		"bad.example\tMAYBE\t/\tFALSE\t0\tn3\tv3\n" + // non-TRUE/FALSE flag
		"bad2.example\tTRUE\t/\tMAYBE\t0\tn4\tv4\n" + // bad secure column
		"bad3.example\tTRUE\t/\tFALSE\tnow\tn5\tv5\n" + // non-integer expiry
		"bad4.example\tTRUE\t/\tFALSE\t-5\tn6\tv6\n" // negative expiry
	out := Parse([]byte(input))
	if len(out) != 2 {
		t.Fatalf("parsed %d rows, want 2 (garbage skipped):\n%v", len(out), out)
	}
	if out[0].Name != "n1" || out[1].Name != "n2" {
		t.Errorf("unexpected rows: %+v", out)
	}
	if !out[1].HttpOnly || out[1].HostOnly {
		t.Errorf("row n2 = %+v, want httpOnly=true hostOnly=false (#HttpOnly_ row)", out[1])
	}
}

func TestCRLFInputAccepted(t *testing.T) {
	input := "example.com\tTRUE\t/\tFALSE\t0\tn\tv\r\n" +
		".c.com\tFALSE\t/p\tTRUE\t5\tm\tw\r\n"
	out := Parse([]byte(input))
	if len(out) != 2 {
		t.Fatalf("parsed %d rows, want 2", len(out))
	}
	if out[0].Name != "n" || out[0].Value != "v" || out[0].HostOnly {
		t.Errorf("row 0 = %+v", out[0])
	}
	if out[1].Secure != true || out[1].Expires.IsZero() {
		t.Errorf("row 1 = %+v (secure/expiry)", out[1])
	}
}

func TestEmptyValueRoundTrip(t *testing.T) {
	// SID= with an empty value is a legal row, NOT a deletion marker in the
	// format layer.
	c := pc(".gog.com", "/", "SID", "", false)
	var buf bytes.Buffer
	if _, err := Write(&buf, []PersistentCookie{c}); err != nil {
		t.Fatal(err)
	}
	out := Parse(buf.Bytes())
	if len(out) != 1 {
		t.Fatalf("parsed %d rows, want 1\n%q", len(out), buf.String())
	}
	if out[0].Value != "" || out[0].Name != "SID" {
		t.Errorf("row = %+v, want SID with empty value", out[0])
	}
}

func TestSessionExpiryZero(t *testing.T) {
	// expiry 0 means a session cookie (no Expires). Non-zero is Unix seconds.
	zero := pc(".a.com", "/", "s", "1", true)
	var buf bytes.Buffer
	if _, err := Write(&buf, []PersistentCookie{zero}); err != nil {
		t.Fatal(err)
	}
	out := Parse(buf.Bytes())
	if !out[0].Expires.IsZero() {
		t.Errorf("session cookie got expires %v", out[0].Expires)
	}
}

func TestTabInValueSkippedAndCounted(t *testing.T) {
	ok := pc(".gog.com", "/", "good", "fine", false)
	bad := pc(".gog.com", "/", "bad", "has\ttab", false)
	newline := pc(".gog.com", "/", "nl", "has\nlf", false)
	cr := pc(".gog.com", "/", "cr", "has\rlf", false)

	var buf bytes.Buffer
	n, err := Write(&buf, []PersistentCookie{ok, bad, newline, cr})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != 1 {
		t.Errorf("written = %d, want 1 (3 unrepresentable skipped)", n)
	}
	out := Parse(buf.Bytes())
	if len(out) != 1 || out[0].Name != "good" {
		t.Errorf("parsed = %+v, want only the representable row", out)
	}
}

func TestSpecialCharactersRoundTrip(t *testing.T) {
	// Values without TAB/CR/LF survive verbatim: spaces, '#', quotes, UTF-8.
	c := pc(".пример.рф", "/a b", "k#1", `va lue "quoted" ¥`, false)
	var buf bytes.Buffer
	if _, err := Write(&buf, []PersistentCookie{c}); err != nil {
		t.Fatal(err)
	}
	out := Parse(buf.Bytes())
	if len(out) != 1 {
		t.Fatalf("parsed %d rows, want 1", len(out))
	}
	checkCookie(t, 0, c, out[0])
}

func TestLargeExpiryRoundTrip(t *testing.T) {
	exp := time.Unix(4102444800, 0) // far future
	c := PersistentCookie{Domain: ".g.com", Path: "/", Name: "n", Value: "v", Expires: exp}
	var buf bytes.Buffer
	if _, err := Write(&buf, []PersistentCookie{c}); err != nil {
		t.Fatal(err)
	}
	out := Parse(buf.Bytes())
	if len(out) != 1 || !out[0].Expires.Equal(exp) {
		t.Errorf("expires round trip failed: %+v", out)
	}
}

func TestWriteHeaderEmittedOnce(t *testing.T) {
	var buf bytes.Buffer
	if _, err := Write(&buf, nil); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != headerLine+"\n" {
		t.Errorf("empty write = %q, want just the header", got)
	}
}

// TestDomainPreservedVerbatim locks the codec's domain transparency: case
// and leading dots survive byte-for-byte. If a future test ever expects
// normalisation here (e.g. "Example.COM" -> "example.com"), that is a
// responsibility leak — domain/host-only/path semantics belong to the
// httpx bridge, never to the format codec.
func TestDomainPreservedVerbatim(t *testing.T) {
	cases := []PersistentCookie{
		pc("Example.COM", "/", "n1", "v1", true),
		pc(".Example.COM", "/", "n2", "v2", false),
		pc(".пример.рф", "/", "n3", "v3", true),
	}
	var buf bytes.Buffer
	if _, err := Write(&buf, cases); err != nil {
		t.Fatal(err)
	}
	out := Parse(buf.Bytes())
	if len(out) != len(cases) {
		t.Fatalf("parsed %d rows, want %d", len(out), len(cases))
	}
	for i, want := range cases {
		if out[i].Domain != want.Domain {
			t.Errorf("row %d domain = %q, want %q (must be verbatim)", i, out[i].Domain, want.Domain)
		}
	}
}
