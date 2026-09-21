package galaxy

import (
	"context"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nekrozis/goggo/internal/config"
)

// depotServer serves one manifest body for every request and counts the calls.
func depotServer(t *testing.T, body string) (*httptest.Server, *int) {
	t.Helper()
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

// depotObject decodes a literal depot document for FiltersDepotItems.
func depotObject(t *testing.T, raw string) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatalf("unmarshal %s: %v", raw, err)
	}
	return v
}

// depotItems runs DepotItems against a manifest literal.
func depotItems(t *testing.T, manifest string, opts DepotOptions) (int, error) {
	t.Helper()
	srv, _ := depotServer(t, manifest)
	cl := newTestClient(t, srv, nil)
	items, err := cl.DepotItems(context.Background(), "abcdef", opts)
	return len(items), err
}

// TestDepotItemsSmallFilesContainer locks
// three-way md5 fallback: the container's own md5, else the single chunk's md5,
// else "".
func TestDepotItemsSmallFilesContainer(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		wantMD5 string
	}{
		{
			name: "container md5 wins",
			body: `{"depot":{"smallFilesContainer":{"md5":"container-md5","chunks":[` +
				`{"compressedMd5":"c1","md5":"u1","compressedSize":10,"size":20},` +
				`{"compressedMd5":"c2","md5":"u2","compressedSize":5,"size":7}]}}}`,
			wantMD5: "container-md5",
		},
		{
			name: "single chunk without container md5",
			body: `{"depot":{"smallFilesContainer":{"chunks":[` +
				`{"compressedMd5":"c1","md5":"u1","compressedSize":10,"size":20}]}}}`,
			wantMD5: "u1",
		},
		{
			name: "several chunks without container md5",
			body: `{"depot":{"smallFilesContainer":{"chunks":[` +
				`{"compressedMd5":"c1","md5":"u1","compressedSize":10,"size":20},` +
				`{"compressedMd5":"c2","md5":"u2","compressedSize":5,"size":7}]}}}`,
			wantMD5: "",
		},
		{
			name: "explicit null container md5 does not fallback to single chunk",
			body: `{"depot":{"smallFilesContainer":{"md5":null,"chunks":[` +
				`{"compressedMd5":"c1","md5":"u1","compressedSize":10,"size":20}]}}}`,
			wantMD5: "",
		},
		{
			name: "empty container md5 does not fallback to single chunk",
			body: `{"depot":{"smallFilesContainer":{"md5":"","chunks":[` +
				`{"compressedMd5":"c1","md5":"u1","compressedSize":10,"size":20}]}}}`,
			wantMD5: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv, _ := depotServer(t, c.body)
			cl := newTestClient(t, srv, nil)
			items, err := cl.DepotItems(context.Background(), "abcdef", DepotOptions{IsDependency: true})
			if err != nil {
				t.Fatalf("DepotItems: %v", err)
			}
			if len(items) != 1 {
				t.Fatalf("items = %d, want the container entry only", len(items))
			}
			it := items[0]
			if it.Path != "galaxy_smallfilescontainer" {
				t.Errorf("path = %q", it.Path)
			}
			if !it.IsSmallFilesContainer {
				t.Error("IsSmallFilesContainer must be set")
			}
			if !it.IsDependency {
				t.Error("IsDependency must be propagated")
			}
			if it.IsInSFC {
				t.Error("the container itself is not inside the container")
			}
			if it.MD5 != c.wantMD5 {
				t.Errorf("md5 = %q, want %q", it.MD5, c.wantMD5)
			}
		})
	}
}

// TestDepotItemsRunningOffsets locks the accumulation: a chunk's offset is where
// the previous chunks ended, and the entry totals are the sums. Order follows the
// document.
func TestDepotItemsRunningOffsets(t *testing.T) {
	const body = `{"depot":{"items":[` +
		`{"path":"a.bin","chunks":[` +
		`{"compressedMd5":"c1","md5":"u1","compressedSize":10,"size":20},` +
		`{"compressedMd5":"c2","md5":"u2","compressedSize":5,"size":7}]},` +
		`{"path":"b.bin","chunks":[{"compressedMd5":"c3","md5":"u3","compressedSize":1,"size":2}]}]}}`
	srv, _ := depotServer(t, body)
	cl := newTestClient(t, srv, nil)
	items, err := cl.DepotItems(context.Background(), "abcdef", DepotOptions{})
	if err != nil {
		t.Fatalf("DepotItems: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}
	if items[0].Path != "a.bin" || items[1].Path != "b.bin" {
		t.Errorf("paths = %q, %q: document order must be kept", items[0].Path, items[1].Path)
	}

	first := items[0]
	if first.Chunks[0].CompressedOffset != 0 || first.Chunks[0].Offset != 0 {
		t.Errorf("chunk 0 offsets = %d/%d, want 0/0",
			first.Chunks[0].CompressedOffset, first.Chunks[0].Offset)
	}
	if first.Chunks[1].CompressedOffset != 10 || first.Chunks[1].Offset != 20 {
		t.Errorf("chunk 1 offsets = %d/%d, want 10/20",
			first.Chunks[1].CompressedOffset, first.Chunks[1].Offset)
	}
	if first.TotalCompressedSize != 15 || first.TotalSize != 27 {
		t.Errorf("totals = %d/%d, want 15/27", first.TotalCompressedSize, first.TotalSize)
	}
	if first.ProductID != "" {
		t.Errorf("ProductID = %q: the reader never stamps it", first.ProductID)
	}
	if first.Chunks[0].CompressedMD5 != "c1" || first.Chunks[0].MD5 != "u1" {
		t.Errorf("chunk md5s = %q/%q", first.Chunks[0].CompressedMD5, first.Chunks[0].MD5)
	}
}

// TestDepotItemsSkipsNonArrayChunks locks the per-entry filter of
// 300: an entry without a chunks array is not an error, it
// is simply not a depot entry.
func TestDepotItemsSkipsNonArrayChunks(t *testing.T) {
	const body = `{"depot":{` +
		`"smallFilesContainer":{"chunks":"nope"},` +
		`"items":[` +
		`{"path":"keep.bin","chunks":[{"compressedMd5":"c1","md5":"u1","compressedSize":1,"size":1}]},` +
		`{"path":"string.bin","chunks":"nope"},` +
		`{"path":"missing.bin"},` +
		`{"path":"null.bin","chunks":null}]}}`
	srv, _ := depotServer(t, body)
	cl := newTestClient(t, srv, nil)
	items, err := cl.DepotItems(context.Background(), "abcdef", DepotOptions{})
	if err != nil {
		t.Fatalf("DepotItems: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want only keep.bin", len(items))
	}
	if items[0].Path != "keep.bin" {
		t.Errorf("path = %q", items[0].Path)
	}
}

// TestDepotItemsSFCRef locks the sfcRef branch of
func TestDepotItemsSFCRef(t *testing.T) {
	const body = `{"depot":{"items":[` +
		`{"path":"in-sfc.bin","chunks":[{"compressedMd5":"c1","md5":"u1","compressedSize":1,"size":1}],` +
		`"sfcRef":{"offset":100,"size":50}},` +
		`{"path":"plain.bin","chunks":[{"compressedMd5":"c2","md5":"u2","compressedSize":2,"size":2}]}]}}`
	srv, _ := depotServer(t, body)
	cl := newTestClient(t, srv, nil)
	items, err := cl.DepotItems(context.Background(), "abcdef", DepotOptions{})
	if err != nil {
		t.Fatalf("DepotItems: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}
	if !items[0].IsInSFC || items[0].SFCOffset != 100 || items[0].SFCSize != 50 {
		t.Errorf("in-sfc item = %+v, want IsInSFC with 100/50", items[0])
	}
	if items[1].IsInSFC || items[1].SFCOffset != 0 || items[1].SFCSize != 0 {
		t.Errorf("plain item = %+v, want no SFC fields", items[1])
	}
}

// TestDepotItemsPathNormalisation locks the path rules: the lowercase rule
// applies only on Windows and only when the setting is on, while the backslash
// rewrite happens in every case.
func TestDepotItemsPathNormalisation(t *testing.T) {
	const body = `{"depot":{"items":[{"path":"Dir\\Sub/File.BIN",` +
		`"chunks":[{"compressedMd5":"c","md5":"u","compressedSize":1,"size":1}]}]}}`
	cases := []struct {
		name string
		opts DepotOptions
		want string
	}{
		{"setting off", DepotOptions{Platform: config.PlatformWindows}, "Dir/Sub/File.BIN"},
		{"on and Windows", DepotOptions{LowercasePaths: true, Platform: config.PlatformWindows}, "dir/sub/file.bin"},
		{"on but Linux", DepotOptions{LowercasePaths: true, Platform: config.PlatformLinux}, "Dir/Sub/File.BIN"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv, _ := depotServer(t, body)
			cl := newTestClient(t, srv, nil)
			items, err := cl.DepotItems(context.Background(), "abcdef", c.opts)
			if err != nil {
				t.Fatalf("DepotItems: %v", err)
			}
			if len(items) != 1 {
				t.Fatalf("items = %d, want 1", len(items))
			}
			if items[0].Path != c.want {
				t.Errorf("path = %q, want %q", items[0].Path, c.want)
			}
		})
	}
}

// TestDepotItemsContainerShape locks the container shape boundary: a missing or
// null section means "not present", a section in the wrong shape is a broken
// document and is reported.
func TestDepotItemsContainerShape(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{"no depot", `{}`, false},
		{"null depot", `{"depot":null}`, false},
		{"array depot", `{"depot":[]}`, true},
		{"no items", `{"depot":{}}`, false},
		{"null items", `{"depot":{"items":null}}`, false},
		{"empty items", `{"depot":{"items":[]}}`, false},
		{"string items", `{"depot":{"items":"nope"}}`, true},
		{"object items", `{"depot":{"items":{}}}`, true},
		{"null container", `{"depot":{"smallFilesContainer":null}}`, false},
		{"string container", `{"depot":{"smallFilesContainer":"nope"}}`, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			n, err := depotItems(t, c.body, DepotOptions{})
			if c.wantErr {
				if err == nil {
					t.Fatal("the document must be reported as broken")
				}
				return
			}
			if err != nil {
				t.Fatalf("DepotItems: %v", err)
			}
			if n != 0 {
				t.Errorf("items = %d, want none", n)
			}
		})
	}
}

// TestFilteredDepotItemsLanguage locks the rule that an empty or missing language
// list selects NOTHING: the flag starts false and is only set on a match.
func TestFilteredDepotItemsLanguage(t *testing.T) {
	cases := []struct {
		name      string
		depot     string
		wantItems int
	}{
		{"regex hit", `{"languages":["en-US"],"manifest":"abcdef"}`, 1},
		{"star hit", `{"languages":["*"],"manifest":"abcdef"}`, 1},
		{"regex miss", `{"languages":["de"],"manifest":"abcdef"}`, 0},
		{"empty list", `{"languages":[],"manifest":"abcdef"}`, 0},
		{"missing list", `{"manifest":"abcdef"}`, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv, calls := depotServer(t, oneChunkManifest)
			cl := newTestClient(t, srv, nil)
			items, err := cl.FilteredDepotItems(context.Background(),
				depotObject(t, c.depot), "en|eng|english|en[_-]US", "64", DepotOptions{})
			if err != nil {
				t.Fatalf("FilteredDepotItems: %v", err)
			}
			if len(items) != c.wantItems {
				t.Errorf("items = %d, want %d", len(items), c.wantItems)
			}
			wantCalls := 0
			if c.wantItems > 0 {
				wantCalls = 1
			}
			if *calls != wantCalls {
				t.Errorf("manifest requests = %d, want %d", *calls, wantCalls)
			}
		})
	}
}

// TestFilteredDepotItemsArch locks the architecture rule: a missing osBitness
// means "not architecture specific" and is selected; a present list must match.
func TestFilteredDepotItemsArch(t *testing.T) {
	cases := []struct {
		name      string
		depot     string
		wantItems int
		wantErr   bool
	}{
		{"missing osBitness", `{"languages":["en"],"manifest":"abcdef"}`, 1, false},
		{"null osBitness", `{"languages":["en"],"osBitness":null,"manifest":"abcdef"}`, 1, false},
		{"arch hit", `{"languages":["en"],"osBitness":["64"],"manifest":"abcdef"}`, 1, false},
		{"star hit", `{"languages":["en"],"osBitness":["*"],"manifest":"abcdef"}`, 1, false},
		{"arch miss", `{"languages":["en"],"osBitness":["32"],"manifest":"abcdef"}`, 0, false},
		{"empty list", `{"languages":["en"],"osBitness":[],"manifest":"abcdef"}`, 0, false},
		{"wrong type", `{"languages":["en"],"osBitness":"64","manifest":"abcdef"}`, 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv, _ := depotServer(t, oneChunkManifest)
			cl := newTestClient(t, srv, nil)
			items, err := cl.FilteredDepotItems(context.Background(),
				depotObject(t, c.depot), "en", "64", DepotOptions{})
			if c.wantErr {
				if err == nil {
					t.Fatal("a broken osBitness must be reported")
				}
				return
			}
			if err != nil {
				t.Fatalf("FilteredDepotItems: %v", err)
			}
			if len(items) != c.wantItems {
				t.Errorf("items = %d, want %d", len(items), c.wantItems)
			}
		})
	}
}

// TestFilteredDepotItemsStampsProductID locks
func TestFilteredDepotItemsStampsProductID(t *testing.T) {
	cases := []struct {
		name       string
		depot      string
		wantProcID string
	}{
		{"stamped", `{"languages":["en"],"productId":"1207659150","manifest":"abcdef"}`, "1207659150"},
		{"left empty", `{"languages":["en"],"manifest":"abcdef"}`, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv, _ := depotServer(t, oneChunkManifest)
			cl := newTestClient(t, srv, nil)
			items, err := cl.FilteredDepotItems(context.Background(),
				depotObject(t, c.depot), "en", "64", DepotOptions{})
			if err != nil {
				t.Fatalf("FilteredDepotItems: %v", err)
			}
			if len(items) != 1 {
				t.Fatalf("items = %d, want 1", len(items))
			}
			if items[0].ProductID != c.wantProcID {
				t.Errorf("ProductID = %q, want %q", items[0].ProductID, c.wantProcID)
			}
		})
	}
}

// TestFilteredDepotItemsInvalidRegex: a pattern that does not compile is reported
// before anything is fetched.
func TestFilteredDepotItemsInvalidRegex(t *testing.T) {
	srv, calls := depotServer(t, oneChunkManifest)
	cl := newTestClient(t, srv, nil)
	_, err := cl.FilteredDepotItems(context.Background(),
		depotObject(t, `{"languages":["en"],"manifest":"abcdef"}`), "(", "64", DepotOptions{})
	if err == nil {
		t.Fatal("an invalid regexp must be reported")
	}
	if *calls != 0 {
		t.Errorf("requests = %d, want 0: the pattern is compiled before any fetch", *calls)
	}
}

// TestDepotItemsRequestsManifest: the expansion goes through the manifest chain,
// so the hash decides the URL.
func TestDepotItemsRequestsManifest(t *testing.T) {
	srv, got := requestRecordingServer(t, `{"depot":{"items":[]}}`)
	cl := newTestClient(t, srv, nil)
	if _, err := cl.DepotItems(context.Background(), "abcdef", DepotOptions{}); err != nil {
		t.Fatalf("DepotItems: %v", err)
	}
	const want = "/content-system/v2/meta/ab/cd/abcdef"
	if *got != want {
		t.Errorf("request URI = %q, want %q", *got, want)
	}
}

// oneChunkManifest is a minimal manifest with a single entry.
const oneChunkManifest = `{"depot":{"items":[{"path":"a.bin",` +
	`"chunks":[{"compressedMd5":"c","md5":"u","compressedSize":1,"size":1}]}]}}`

// TestReadDepotSize locks the reader's rules: strings, booleans, negative numbers,
// and floats are rejected rather than coerced.
func TestReadDepotSize(t *testing.T) {
	cases := []struct {
		name    string
		value   jsontext.Value
		want    uint64
		wantErr bool
	}{
		{"whole number", jsontext.Value("5"), 5, false},
		{"zero", jsontext.Value("0"), 0, false},
		{"nil is zero", nil, 0, false},
		{"null literal is zero", jsontext.Value("null"), 0, false},
		{"empty is zero", jsontext.Value(""), 0, false},
		{"large 64-bit precision", jsontext.Value("58812465975493914"), 58812465975493914, false},
		{"max uint64", jsontext.Value("18446744073709551615"), 18446744073709551615, false},
		{"negative number", jsontext.Value("-1"), 0, true},
		{"fractional", jsontext.Value("5.5"), 0, true},
		{"string number", jsontext.Value(`"5"`), 0, true},
		{"boolean", jsontext.Value("true"), 0, true},
		{"array", jsontext.Value("[]"), 0, true},
		{"object", jsontext.Value("{}"), 0, true},
		{"overflow past uint64", jsontext.Value("18446744073709551616"), 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := readDepotSize(c.value, "size")
			if c.wantErr {
				if err == nil {
					t.Fatalf("readDepotSize(%s) must fail", string(c.value))
				}
				return
			}
			if err != nil {
				t.Fatalf("readDepotSize(%s): %v", string(c.value), err)
			}
			if got != c.want {
				t.Errorf("readDepotSize(%s) = %d, want %d", string(c.value), got, c.want)
			}
		})
	}
}

// TestDepotItemsRejectsBrokenChunkFields: a chunk whose size is a string is a
// protocol error, not a value to coerce.
func TestDepotItemsRejectsBrokenChunkFields(t *testing.T) {
	const body = `{"depot":{"items":[{"path":"a.bin",` +
		`"chunks":[{"compressedMd5":"c","md5":"u","compressedSize":"1","size":1}]}]}}`
	_, err := depotItems(t, body, DepotOptions{})
	if err == nil {
		t.Fatal("a string byte count must be reported")
	}
	if !strings.Contains(err.Error(), "compressedSize") {
		t.Errorf("err = %q, want it to name the field", err)
	}
}

// TestDepotItemsPropagatesManifestFailure: the HTTP error of the manifest fetch
// is not swallowed.
func TestDepotItemsPropagatesManifestFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cl := newTestClient(t, srv, nil)
	_, err := cl.DepotItems(context.Background(), "abcdef", DepotOptions{})
	if err == nil {
		t.Fatal("a failing manifest fetch must be reported")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("err = %q, want the status", err)
	}
	if errors.Is(err, ErrNotJSON) {
		t.Errorf("err = %v must stay an HTTP error", err)
	}
}

// TestDepotItems64BitPrecision locks the 64-bit unsigned integer decoding across
// the full DepotItems pipeline, verifying that chunk sizes, running offsets,
// item totals, and sfcRef ranges preserve exact values > 2^53 without float64 loss.
func TestDepotItems64BitPrecision(t *testing.T) {
	// 58812465975493914 > 2^53, exercises the path that would lose
	// precision if decoded through float64 (58812465975493920).
	const body = `{"depot":{"items":[{` +
		`"path":"large.bin",` +
		`"chunks":[` +
		`{"compressedMd5":"c1","md5":"u1","compressedSize":58812465975493914,"size":58812465975493914},` +
		`{"compressedMd5":"c2","md5":"u2","compressedSize":2,"size":3}` +
		`],` +
		`"sfcRef":{"offset":58812465975493914,"size":2}` +
		`}]}}`

	srv, _ := depotServer(t, body)
	cl := newTestClient(t, srv, nil)
	items, err := cl.DepotItems(context.Background(), "abcdef", DepotOptions{})
	if err != nil {
		t.Fatalf("DepotItems: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(items))
	}
	item := items[0]
	if len(item.Chunks) != 2 {
		t.Fatalf("len(item.Chunks) = %d, want 2", len(item.Chunks))
	}

	// Chunk 0 exact sizes and initial 0 offsets.
	if item.Chunks[0].CompressedSize != 58812465975493914 {
		t.Errorf("chunk 0 CompressedSize = %d, want 58812465975493914", item.Chunks[0].CompressedSize)
	}
	if item.Chunks[0].Size != 58812465975493914 {
		t.Errorf("chunk 0 Size = %d, want 58812465975493914", item.Chunks[0].Size)
	}
	if item.Chunks[0].CompressedOffset != 0 || item.Chunks[0].Offset != 0 {
		t.Errorf("chunk 0 offsets = %d/%d, want 0/0", item.Chunks[0].CompressedOffset, item.Chunks[0].Offset)
	}

	// Chunk 1 running offset accumulation > 2^53.
	if item.Chunks[1].CompressedOffset != 58812465975493914 {
		t.Errorf("chunk 1 CompressedOffset = %d, want 58812465975493914", item.Chunks[1].CompressedOffset)
	}
	if item.Chunks[1].Offset != 58812465975493914 {
		t.Errorf("chunk 1 Offset = %d, want 58812465975493914", item.Chunks[1].Offset)
	}

	// Item totals accumulation.
	const wantTotalCompressed = 58812465975493916 // 58812465975493914 + 2
	const wantTotal = 58812465975493917           // 58812465975493914 + 3
	if item.TotalCompressedSize != wantTotalCompressed {
		t.Errorf("TotalCompressedSize = %d, want %d", item.TotalCompressedSize, wantTotalCompressed)
	}
	if item.TotalSize != wantTotal {
		t.Errorf("TotalSize = %d, want %d", item.TotalSize, wantTotal)
	}

	// sfcRef exact offsets > 2^53.
	if !item.IsInSFC {
		t.Error("item.IsInSFC must be true")
	}
	if item.SFCOffset != 58812465975493914 {
		t.Errorf("item.SFCOffset = %d, want 58812465975493914", item.SFCOffset)
	}
	if item.SFCSize != 2 {
		t.Errorf("item.SFCSize = %d, want 2", item.SFCSize)
	}
}

// TestDepotItemsRejectsNonStringIdentifiers locks the boundary tightening for
// path and chunk hash fields: numbers and booleans are rejected as protocol errors.
func TestDepotItemsRejectsNonStringIdentifiers(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{
			name:    "number path rejected",
			body:    `{"depot":{"items":[{"path":12345,"chunks":[{"compressedMd5":"c","md5":"u","compressedSize":1,"size":1}]}]}}`,
			wantErr: true,
		},
		{
			name:    "boolean path rejected",
			body:    `{"depot":{"items":[{"path":true,"chunks":[{"compressedMd5":"c","md5":"u","compressedSize":1,"size":1}]}]}}`,
			wantErr: true,
		},
		{
			name:    "number chunk md5 rejected",
			body:    `{"depot":{"items":[{"path":"a.bin","chunks":[{"compressedMd5":"c","md5":12345,"compressedSize":1,"size":1}]}]}}`,
			wantErr: true,
		},
		{
			name:    "boolean chunk compressedMd5 rejected",
			body:    `{"depot":{"items":[{"path":"a.bin","chunks":[{"compressedMd5":true,"md5":"u","compressedSize":1,"size":1}]}]}}`,
			wantErr: true,
		},
		{
			name:    "number item md5 rejected",
			body:    `{"depot":{"items":[{"path":"a.bin","md5":99999,"chunks":[{"compressedMd5":"c","md5":"u","compressedSize":1,"size":1}]}]}}`,
			wantErr: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := depotItems(t, c.body, DepotOptions{})
			if c.wantErr && err == nil {
				t.Fatalf("expected error for non-string identifier in %s", c.name)
			}
		})
	}
}
