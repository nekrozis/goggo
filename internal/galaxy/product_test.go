package galaxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// productFixture answers product requests and remembers what it was asked, so a
// test can assert both the document that came back and the requests that
// produced it.
type productFixture struct {
	*httptest.Server

	mu    sync.Mutex
	seen  []string
	serve func(uri string) (string, int)
}

func newProductFixture(t *testing.T) *productFixture {
	t.Helper()
	f := &productFixture{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uri := r.URL.RequestURI()
		f.mu.Lock()
		f.seen = append(f.seen, uri)
		serve := f.serve
		f.mu.Unlock()
		body, status := serve(uri)
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *productFixture) setServe(serve func(uri string) (string, int)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.serve = serve
}

func (f *productFixture) requestURIs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.seen...)
}

// newProductClient points every endpoint of a Client at the fixture: the api
// host is where Product lives, and leaving it empty would build a relative url.
func newProductClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	cl := newTestClient(t, srv, nil)
	cl.ep.api = srv.URL
	return cl
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	return string(b)
}

// dlcIDs digs the ids= list out of a batch request, in the order it was sent.
func dlcIDs(uri string) []string {
	start := strings.Index(uri, "ids=")
	if start < 0 {
		return nil
	}
	rest := uri[start+len("ids="):]
	if end := strings.Index(rest, "&"); end >= 0 {
		rest = rest[:end]
	}
	return strings.Split(rest, ",")
}

// TestProductSkipsExpansionWithoutDLCInformation locks the first half of the
// dlcs shape rule: absent and null both mean "no DLC information", so no
// document is fetched and no member is invented. A null is how an optional
// member is commonly spelled — treating it as malformed would turn a normal
// answer into a failure.
func TestProductSkipsExpansionWithoutDLCInformation(t *testing.T) {
	for _, tc := range []struct {
		name string
		doc  string
	}{
		{"absent", `{"id":"1","slug":"game"}`},
		{"null", `{"id":"1","slug":"game","dlcs":null}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newProductFixture(t)
			f.setServe(func(string) (string, int) { return tc.doc, http.StatusOK })

			got, err := newProductClient(t, f.Server).Product(context.Background(), "1")
			if err != nil {
				t.Fatalf("Product: %v", err)
			}
			if !bytes.Equal(got.JSON, []byte(tc.doc)) {
				t.Fatalf("JSON was rewritten without DLCs: got %q, want %q", got.JSON, tc.doc)
			}
			if bytes.Contains(got.JSON, []byte(`"expanded_dlcs"`)) {
				t.Errorf("expanded_dlcs present in JSON, want absent: %s", got.JSON)
			}
			if got.Slug != "game" {
				t.Errorf("Slug = %q, want game", got.Slug)
			}
			if uris := f.requestURIs(); len(uris) != 1 {
				t.Errorf("requests = %v, want only the product document", uris)
			}
		})
	}
}

// TestProductSkipsExpansionForNonObjectDLCs locks the dlcs guard: a dlcs member
// that is present but not an object — the empty array the live API really sends
// for products without DLC information, a filled array, or any scalar — does not
// enter the expansion block: no request, no expanded_dlcs, and the product
// document comes back as the API answered it.
//
// This test REVERSES the earlier TestProductRejectsAMalformedDLCsMember, which
// asserted an error here. That behaviour rejected 8 of 14 probed account products
// at the Product call, and the live API proved the difference is not academic.
func TestProductSkipsExpansionForNonObjectDLCs(t *testing.T) {
	for _, tc := range []struct {
		name string
		dlcs any
	}{
		{"empty array", []any{}},
		{"non-empty array", []any{map[string]any{"id": "2"}}},
		{"string", "oops"},
		{"number", float64(123)},
		{"boolean", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newProductFixture(t)
			wantDoc := mustJSON(t, map[string]any{"id": "1", "slug": "game", "dlcs": tc.dlcs})
			f.setServe(func(string) (string, int) {
				return wantDoc, http.StatusOK
			})

			got, err := newProductClient(t, f.Server).Product(context.Background(), "1")
			if err != nil {
				t.Fatalf("Product must skip a non-object dlcs, not fail: %v", err)
			}
			if !bytes.Equal(got.JSON, []byte(wantDoc)) {
				t.Fatalf("JSON was rewritten for non-object DLCs: got %q, want %q", got.JSON, wantDoc)
			}
			if bytes.Contains(got.JSON, []byte(`"expanded_dlcs"`)) {
				t.Errorf("expanded_dlcs present in JSON, want absent (nothing was expanded)")
			}
			// The document is untouched apart from the missing injection: the
			// guard is skipped and the response is returned as it arrived.
			if got.Slug != "game" {
				t.Errorf("slug = %v, want the original value", got.Slug)
			}
			if uris := f.requestURIs(); len(uris) != 1 {
				t.Errorf("requests = %v, want only the main document", uris)
			}
		})
	}
}

// TestProductExpandsDLCsInOneRequest locks the single-request branch: at most
// maxDLCBatchSize ids are all fetched in one request to the url the document
// advertises, and the response array is what lands under expanded_dlcs.
func TestProductExpandsDLCsInOneRequest(t *testing.T) {
	f := newProductFixture(t)
	expansion := `[{"id":"2"},{"id":"3"}]`
	f.setServe(func(uri string) (string, int) {
		if strings.HasPrefix(uri, "/products/1") {
			return mustJSON(t, map[string]any{
				"id":   "1",
				"slug": "game",
				"dlcs": map[string]any{
					"products":                  []any{map[string]any{"id": "2"}, map[string]any{"id": "3"}},
					"expanded_all_products_url": f.Server.URL + "/expanded",
				},
			}), http.StatusOK
		}
		return expansion, http.StatusOK
	})

	got, err := newProductClient(t, f.Server).Product(context.Background(), "1")
	if err != nil {
		t.Fatalf("Product: %v", err)
	}
	var doc struct {
		ExpandedDLCs []struct {
			ID string `json:"id"`
		} `json:"expanded_dlcs"`
	}
	if err := json.Unmarshal(got.JSON, &doc); err != nil {
		t.Fatalf("unmarshal got.JSON: %v", err)
	}
	if len(doc.ExpandedDLCs) != 2 {
		t.Fatalf("expanded_dlcs = %+v, want 2", doc.ExpandedDLCs)
	}
	if uris := f.requestURIs(); len(uris) != 2 || uris[1] != "/expanded" {
		t.Errorf("requests = %v, want the document then /expanded", uris)
	}
	// The rest of the document is untouched: Product hands back what the API
	// answered, it does not rebuild it.
	if got.Slug != "game" {
		t.Errorf("slug = %v, want the original value", got.Slug)
	}
}

// TestProductBatchesDLCIDs locks the second branch: the
// ids go out in batches of 45, the responses are appended in order, and a count
// that is an exact multiple of the batch size does not send a trailing empty
// request.
func TestProductBatchesDLCIDs(t *testing.T) {
	for _, tc := range []struct {
		count   int
		batches []int
	}{
		{count: 46, batches: []int{45, 1}},
		{count: 90, batches: []int{45, 45}},
		{count: 91, batches: []int{45, 45, 1}},
	} {
		t.Run(fmt.Sprintf("%d ids", tc.count), func(t *testing.T) {
			ids := make([]string, tc.count)
			entries := make([]any, tc.count)
			for i := range ids {
				ids[i] = fmt.Sprintf("dlc%03d", i)
				entries[i] = map[string]any{"id": ids[i]}
			}

			f := newProductFixture(t)
			f.setServe(func(uri string) (string, int) {
				if strings.HasPrefix(uri, "/products?ids=") {
					// Echo the batch back as documents, so the order and the
					// batching are both visible in the result.
					got := dlcIDs(uri)
					docs := make([]any, len(got))
					for i, id := range got {
						docs[i] = map[string]any{"id": id}
					}
					return mustJSON(t, docs), http.StatusOK
				}
				return mustJSON(t, map[string]any{
					"id": "1",
					"dlcs": map[string]any{
						"products": entries,
						// Present but never used on this branch: the batch url is
						// built here, not read from the document.
						"expanded_all_products_url": f.Server.URL + "/expanded",
					},
				}), http.StatusOK
			})

			got, err := newProductClient(t, f.Server).Product(context.Background(), "1")
			if err != nil {
				t.Fatalf("Product: %v", err)
			}

			var sizes []int
			for _, uri := range f.requestURIs() {
				if strings.HasPrefix(uri, "/products?ids=") {
					sizes = append(sizes, len(dlcIDs(uri)))
				}
			}
			if fmt.Sprint(sizes) != fmt.Sprint(tc.batches) {
				t.Errorf("batch sizes = %v, want %v", sizes, tc.batches)
			}

			var doc struct {
				ExpandedDLCs []struct {
					ID string `json:"id"`
				} `json:"expanded_dlcs"`
			}
			if err := json.Unmarshal(got.JSON, &doc); err != nil {
				t.Fatalf("unmarshal got.JSON: %v", err)
			}
			if len(doc.ExpandedDLCs) != tc.count {
				t.Fatalf("expanded_dlcs has %d entries, want %d", len(doc.ExpandedDLCs), tc.count)
			}
			for i, entry := range doc.ExpandedDLCs {
				if entry.ID != ids[i] {
					t.Fatalf("expanded_dlcs[%d].id = %v, want %v (order must survive batching)", i, entry.ID, ids[i])
				}
			}
		})
	}
}

// TestProductExpandsNumericDLCIDs locks the numeric identifier read: the live
// API sends dlcs.products ids as JSON numbers, and that read only happens on the
// batching branch — more than maxDLCBatchSize ids — so this fixture crosses the
// 45 boundary on purpose. The ids= list must carry the numbers stringified, in
// order, without floating-point precision loss.
func TestProductExpandsNumericDLCIDs(t *testing.T) {
	const total = maxDLCBatchSize + 1
	entries := make([]any, 0, total)
	for i := 0; i < total; i++ {
		switch i {
		case 0:
			entries = append(entries, map[string]any{"id": 1523284508})
		case 1:
			// 58812465975493914 > 2^53, exercises the path that would lose
			// precision if decoded through float64.
			entries = append(entries, map[string]any{"id": 58812465975493914})
		default:
			entries = append(entries, map[string]any{"id": fmt.Sprintf("s%03d", i)})
		}
	}

	f := newProductFixture(t)
	f.setServe(func(uri string) (string, int) {
		if strings.HasPrefix(uri, "/products?ids=") {
			got := dlcIDs(uri)
			docs := make([]any, len(got))
			for i, id := range got {
				docs[i] = map[string]any{"id": id}
			}
			return mustJSON(t, docs), http.StatusOK
		}
		return mustJSON(t, map[string]any{
			"id": "1",
			"dlcs": map[string]any{
				"products": entries,
				// Present but never used on this branch.
				"expanded_all_products_url": "unused",
			},
		}), http.StatusOK
	})

	got, err := newProductClient(t, f.Server).Product(context.Background(), "1")
	if err != nil {
		t.Fatalf("Product with numeric dlc ids: %v", err)
	}
	var batches [][]string
	for _, uri := range f.requestURIs() {
		if strings.HasPrefix(uri, "/products?ids=") {
			batches = append(batches, dlcIDs(uri))
		}
	}
	if len(batches) != 2 || len(batches[0]) != maxDLCBatchSize || len(batches[1]) != 1 {
		t.Fatalf("batches = %v, want %d then 1", batches, maxDLCBatchSize)
	}
	if batches[0][0] != "1523284508" || batches[0][1] != "58812465975493914" {
		t.Errorf("first ids = %q,%q, want the numeric ids stringified without precision loss", batches[0][0], batches[0][1])
	}
	var doc struct {
		ExpandedDLCs []struct {
			ID any `json:"id"`
		} `json:"expanded_dlcs"`
	}
	if err := json.Unmarshal(got.JSON, &doc); err != nil {
		t.Fatalf("unmarshal got.JSON: %v", err)
	}
	if len(doc.ExpandedDLCs) != total {
		t.Fatalf("expanded_dlcs has %d entries, want %d documents", len(doc.ExpandedDLCs), total)
	}
}

// TestProductRejectsInvalidDLCID locks the identifier read rules: structured
// values (objects, arrays) and booleans are refused instead of being converted into
// an arbitrary string representation. The read lives on the batching branch, so the
// entry list crosses the boundary.
func TestProductRejectsInvalidDLCID(t *testing.T) {
	for _, tc := range []struct {
		name string
		val  any
	}{
		{"object", map[string]any{}},
		{"array", []any{1, 2}},
		{"boolean true", true},
		{"boolean false", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entries := make([]any, 0, maxDLCBatchSize+1)
			entries = append(entries, map[string]any{"id": tc.val})
			for i := 1; i <= maxDLCBatchSize; i++ {
				entries = append(entries, map[string]any{"id": fmt.Sprintf("s%03d", i)})
			}

			f := newProductFixture(t)
			f.setServe(func(string) (string, int) {
				return mustJSON(t, map[string]any{"id": "1", "dlcs": map[string]any{
					"products":                  entries,
					"expanded_all_products_url": "unused",
				}}), http.StatusOK
			})
			_, err := newProductClient(t, f.Server).Product(context.Background(), "1")
			if err == nil {
				t.Fatalf("Product must reject %s dlc id", tc.name)
			}
			if !strings.Contains(err.Error(), "dlcs.products[0].id") {
				t.Errorf("error = %v, want it to name the entry", err)
			}
			if uris := f.requestURIs(); len(uris) != 1 {
				t.Errorf("requests = %v, want no batch attempt", uris)
			}
		})
	}
}

// TestProductSkipsTheRequestForAnUnusableExpansionURL locks the defined case: an
// empty (or missing) expanded_all_products_url is not requested, it produces an
// empty result.
func TestProductSkipsTheRequestForAnUnusableExpansionURL(t *testing.T) {
	for _, tc := range []struct {
		name string
		dlcs map[string]any
	}{
		{"empty url", map[string]any{"products": []any{map[string]any{"id": "2"}}, "expanded_all_products_url": ""}},
		{"absent url", map[string]any{"products": []any{map[string]any{"id": "2"}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newProductFixture(t)
			f.setServe(func(string) (string, int) {
				return mustJSON(t, map[string]any{"id": "1", "dlcs": tc.dlcs}), http.StatusOK
			})

			got, err := newProductClient(t, f.Server).Product(context.Background(), "1")
			if err != nil {
				t.Fatalf("Product: %v", err)
			}
			var doc struct {
				ExpandedDLCs []any `json:"expanded_dlcs"`
			}
			if err := json.Unmarshal(got.JSON, &doc); err != nil {
				t.Fatalf("unmarshal got.JSON: %v", err)
			}
			if doc.ExpandedDLCs == nil || len(doc.ExpandedDLCs) != 0 {
				t.Errorf("expanded_dlcs = %v, want an empty array", doc.ExpandedDLCs)
			}
			if uris := f.requestURIs(); len(uris) != 1 {
				t.Errorf("requests = %v, want no request for an unusable url", uris)
			}
		})
	}
}

// TestProductRejectsANonArrayExpansion locks the shape gate on the expansion
// response: the array the API documents is required, not whatever JSON came
// back.
func TestProductRejectsANonArrayExpansion(t *testing.T) {
	f := newProductFixture(t)
	f.setServe(func(uri string) (string, int) {
		if strings.HasPrefix(uri, "/products/1") {
			return mustJSON(t, map[string]any{
				"id": "1",
				"dlcs": map[string]any{
					"products":                  []any{map[string]any{"id": "2"}},
					"expanded_all_products_url": f.Server.URL + "/expanded",
				},
			}), http.StatusOK
		}
		return `{"id":"2"}`, http.StatusOK
	})

	_, err := newProductClient(t, f.Server).Product(context.Background(), "1")
	if err == nil {
		t.Fatal("an object expansion response must fail")
	}
	if !strings.Contains(err.Error(), expandedDLCsKey) {
		t.Errorf("error = %v, want it to name %s", err, expandedDLCsKey)
	}
}

// TestProductRequestShapes locks the two urls byte for byte: the expand list is
// the documented one, and the batch ids are joined with commas in order, without
// any re-encoding or sorting.
func TestProductRequestShapes(t *testing.T) {
	f := newProductFixture(t)
	f.setServe(func(uri string) (string, int) {
		if strings.HasPrefix(uri, "/products?ids=") {
			return `[]`, http.StatusOK
		}
		if strings.HasPrefix(uri, "/products/1?") {
			entries := make([]any, 0, 46)
			for i := 0; i < 46; i++ {
				entries = append(entries, map[string]any{"id": fmt.Sprintf("d%d", i)})
			}
			return mustJSON(t, map[string]any{"id": "1", "dlcs": map[string]any{"products": entries}}), http.StatusOK
		}
		return `{}`, http.StatusOK
	})

	if _, err := newProductClient(t, f.Server).Product(context.Background(), "1"); err != nil {
		t.Fatalf("Product: %v", err)
	}
	uris := f.requestURIs()
	want := []string{
		"/products/1?expand=" + productExpand,
		"/products?ids=" + strings.Join(idsRange(0, 45), ",") + "&expand=" + productExpand,
		"/products?ids=d45&expand=" + productExpand,
	}
	if fmt.Sprint(uris) != fmt.Sprint(want) {
		t.Errorf("requests =\n  %v\nwant\n  %v", uris, want)
	}
}

func idsRange(from, to int) []string {
	out := make([]string, 0, to-from)
	for i := from; i < to; i++ {
		out = append(out, fmt.Sprintf("d%d", i))
	}
	return out
}

// TestProductRejectsANonObjectDocument keeps the main document's contract: the
// shape a caller gets from every other endpoint of this package is what Error
// there is here too.
func TestProductRejectsANonObjectDocument(t *testing.T) {
	f := newProductFixture(t)
	f.setServe(func(string) (string, int) { return `[{"id":"1"}]`, http.StatusOK })

	_, err := newProductClient(t, f.Server).Product(context.Background(), "1")
	if !errors.Is(err, ErrNotJSON) {
		t.Errorf("Product on an array = %v, want ErrNotJSON", err)
	}
}

// TestDecodeDocumentTakesArraysWhileResponseJSONRefusesThem locks both sides of
// the split: the product expansion reads a top-level array, and the object-shaped
// entry point every other caller uses is not loosened by that — the very same
// body still fails it.
func TestDecodeDocumentTakesArraysWhileResponseJSONRefusesThem(t *testing.T) {
	const body = `[{"id":"2"}]`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body))
	}))
	defer srv.Close()

	v, err := decodeDocument(body)
	if err != nil {
		t.Fatalf("decodeDocument(array) = %v, want success", err)
	}
	if _, ok := v.([]any); !ok {
		t.Errorf("decodeDocument(array) = %T, want []any", v)
	}

	if _, err := decodeJSONObject(body); !errors.Is(err, ErrNotJSON) {
		t.Errorf("decodeJSONObject(array) = %v, want ErrNotJSON", err)
	}

	cl := newProductClient(t, srv)
	if _, err := cl.ResponseJSON(context.Background(), srv.URL+"/dlc"); !errors.Is(err, ErrNotJSON) {
		t.Errorf("ResponseJSON on an array = %v, want ErrNotJSON", err)
	}
}

// TestDecodeDocumentHandlesZlibArraysAndObjects locks the zlib retry moving up
// a level: both container shapes decode through it, and ResponseJSON keeps
// refusing the array one — now because of its own assertion rather than
// because the inflation never happened.
func TestDecodeDocumentHandlesZlibArraysAndObjects(t *testing.T) {
	v, err := decodeDocument(zlibBody(t, `[{"id":"2"}]`))
	if err != nil {
		t.Fatalf("decodeDocument(zlib array) = %v, want success", err)
	}
	if _, ok := v.([]any); !ok {
		t.Errorf("decodeDocument(zlib array) = %T, want []any", v)
	}

	if _, err := decodeJSONObject(zlibBody(t, `[{"id":"2"}]`)); !errors.Is(err, ErrNotJSON) {
		t.Errorf("decodeJSONObject(zlib array) = %v, want ErrNotJSON", err)
	}

	obj, err := decodeDocument(zlibBody(t, `{"items":[]}`))
	if err != nil {
		t.Fatalf("decodeDocument(zlib object) = %v, want success", err)
	}
	if _, ok := obj.(map[string]any); !ok {
		t.Errorf("decodeDocument(zlib object) = %T, want map[string]any", obj)
	}
}

// TestProductPreservesDocumentFidelity tests that:
//  1. Without DLC expansion, the exact response bytes (including custom whitespace,
//     formatting, and unknown fields) are preserved byte-for-byte.
//  2. With DLC expansion, top-level unknown fields and 64-bit integer values in the
//     parent document are preserved without float64 precision degradation.
func TestProductPreservesDocumentFidelity(t *testing.T) {
	t.Run("byte-for-byte fidelity without expansion", func(t *testing.T) {
		const rawDoc = "  {\n\t\"id\": 58812465975493914,\n\t\"slug\": \"fidelity-test\",\n\t\"title\": \"Fidelity Test\",\n\t\"extra_metric\": 9007199254740993,\n\t\"dlcs\": []\n  }"
		f := newProductFixture(t)
		f.setServe(func(string) (string, int) { return rawDoc, http.StatusOK })

		got, err := newProductClient(t, f.Server).Product(context.Background(), "1")
		if err != nil {
			t.Fatalf("Product: %v", err)
		}
		if !bytes.Equal(got.JSON, []byte(rawDoc)) {
			t.Fatalf("got.JSON != rawDoc:\ngot:  %q\nwant: %q", got.JSON, rawDoc)
		}
		if got.Slug != "fidelity-test" {
			t.Errorf("Slug = %q, want fidelity-test", got.Slug)
		}
		if got.Title != "Fidelity Test" {
			t.Errorf("Title = %q, want Fidelity Test", got.Title)
		}
	})

	t.Run("field preservation with expansion", func(t *testing.T) {
		f := newProductFixture(t)
		f.setServe(func(uri string) (string, int) {
			if strings.HasPrefix(uri, "/products/1") {
				return fmt.Sprintf(`{"id":58812465975493914,"slug":"witcher-3","title":"The Witcher 3","unknown_large_int":9007199254740993,"dlcs":{"products":[{"id":"dlc-1"}],"expanded_all_products_url":"%s/expanded"}}`, f.Server.URL), http.StatusOK
			}
			return `[{"id":"dlc-1","title":"Hearts of Stone"}]`, http.StatusOK
		})

		got, err := newProductClient(t, f.Server).Product(context.Background(), "1")
		if err != nil {
			t.Fatalf("Product: %v", err)
		}
		if got.Slug != "witcher-3" {
			t.Errorf("Slug = %q, want witcher-3", got.Slug)
		}
		if got.Title != "The Witcher 3" {
			t.Errorf("Title = %q, want The Witcher 3", got.Title)
		}

		// Ensure 64-bit integer was not converted to float64 (58812465975493920 / 9007199254740992)
		jsonStr := string(got.JSON)
		if !strings.Contains(jsonStr, "58812465975493914") {
			t.Errorf("got.JSON does not contain 58812465975493914 verbatim: %s", jsonStr)
		}
		if !strings.Contains(jsonStr, "9007199254740993") {
			t.Errorf("got.JSON does not contain 9007199254740993 verbatim: %s", jsonStr)
		}
		if !strings.Contains(jsonStr, `"expanded_dlcs"`) {
			t.Errorf("got.JSON does not contain expanded_dlcs: %s", jsonStr)
		}
	})
}

// TestProductRejectsMalformedDocument locks boundary errors on empty, whitespace,
// or non-JSON payloads.
//
// Pure empty body produces an ErrNotJSON wrapping "empty body"; JSON whitespace,
// non-JSON whitespace (such as NBSP), and syntax errors are rejected by the decoder
// rather than classified as empty bodies.
func TestProductRejectsMalformedDocument(t *testing.T) {
	for _, tc := range []struct {
		name         string
		body         string
		wantEmptyErr bool
	}{
		{"empty body", "", true},
		{"whitespace only", "   \n\t  ", false},
		{"NBSP only", "\u00a0", false},
		{"NBSP with JSON", "\u00a0{\"slug\":\"game\"}", false},
		{"malformed JSON", "{not-valid-json", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newProductFixture(t)
			f.setServe(func(string) (string, int) { return tc.body, http.StatusOK })

			_, err := newProductClient(t, f.Server).Product(context.Background(), "1")
			if !errors.Is(err, ErrNotJSON) {
				t.Fatalf("Product on %s: err = %v, want ErrNotJSON", tc.name, err)
			}
			hasEmpty := strings.Contains(err.Error(), "empty body")
			if tc.wantEmptyErr && !hasEmpty {
				t.Errorf("Product on %s: err = %v, want 'empty body'", tc.name, err)
			}
			if !tc.wantEmptyErr && hasEmpty {
				t.Errorf("Product on %s: err = %v, must not be classified as 'empty body'", tc.name, err)
			}
		})
	}
}
