package galaxy

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestProductBuildsRequest locks the URL construction against
func TestProductBuildsRequest(t *testing.T) {
	cases := []struct {
		name       string
		productID  string
		platform   string
		generation string
		wantPath   string
		wantQuery  string
	}{
		{
			name:      "explicit platform and generation",
			productID: "1207659150", platform: "linux", generation: "2",
			wantPath: "/products/1207659150/os/linux/builds", wantQuery: "generation=2",
		},
		{
			name:      "empty values fall back to the defaults",
			productID: "1207659150", platform: "", generation: "",
			wantPath: "/products/1207659150/os/windows/builds", wantQuery: "generation=2",
		},
		{
			name:      "generation 1 is passed through",
			productID: "12345", platform: "osx", generation: "1",
			wantPath: "/products/12345/os/osx/builds", wantQuery: "generation=1",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var gotPath, gotQuery string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
				fmt.Fprint(w, `{"items":[]}`)
			}))
			defer srv.Close()

			cl := newTestClient(t, srv, nil)
			if _, err := cl.ProductBuilds(context.Background(), c.productID, c.platform, c.generation); err != nil {
				t.Fatalf("ProductBuilds: %v", err)
			}
			if gotPath != c.wantPath {
				t.Errorf("path = %q, want %q", gotPath, c.wantPath)
			}
			if gotQuery != c.wantQuery {
				t.Errorf("query = %q, want %q", gotQuery, c.wantQuery)
			}
		})
	}
}

// TestProductBuildsReturnsRawDocument locks the return-type contract: the
// document comes back navigable as decoded JSON, so the fields the download
// path reads ( items[].generation and
// items[].link) are reachable without a schema imposed here.
func TestProductBuildsReturnsRawDocument(t *testing.T) {
	const body = `{"items":[{"generation":2,` +
		`"link":"https://cdn.gog.com/content-system/v2/meta/ab/cd/abcdef"}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, body)
	}))
	defer srv.Close()

	cl := newTestClient(t, srv, nil)
	doc, err := cl.ProductBuilds(context.Background(), "1207659150", "windows", "2")
	if err != nil {
		t.Fatalf("ProductBuilds: %v", err)
	}
	items := mustArray(t, doc["items"])
	if len(items) != 1 {
		t.Fatalf("items = %#v, want one element", items)
	}
	item := mustObject(t, items[0])
	if got := mustInt(t, item["generation"]); got != 2 {
		t.Errorf("generation = %d, want 2", got)
	}
	if got := mustText(t, item["link"]); got == "" {
		t.Errorf("link = %q, want the build link", got)
	}
}

// TestProductBuildsRejectsNonObjectDocument: the shape contract applies to the
// real call too — an array is not a builds document.
func TestProductBuildsRejectsNonObjectDocument(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"generation":2}]`)
	}))
	defer srv.Close()

	cl := newTestClient(t, srv, nil)
	if _, err := cl.ProductBuilds(context.Background(), "1", "", ""); err == nil {
		t.Fatal("an array body must not be accepted as a builds document")
	}
}
