package webapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nekrozis/goggo/internal/httpx"
)

// productServer serves one JSON body for every request, capturing the last
// request URI.
func productServer(t *testing.T, body string, status int) (*httptest.Server, *string) {
	t.Helper()
	var lastURI string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastURI = r.URL.RequestURI()
		if status != http.StatusOK {
			http.Error(w, "boom", status)
			return
		}
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, &lastURI
}

func TestFilteredProductsPageRendersQueryVerbatim(t *testing.T) {
	srv, lastURI := productServer(t, `{"page":1,"totalPages":1,"products":[]}`, http.StatusOK)
	cl, _ := newTestClient(t, srv, 0)

	q := ProductQuery{Tags: []string{"gog", "wishlist"}, HiddenFlag: 1, IsUpdated: 1}
	if _, err := cl.FilteredProductsPage(context.Background(), q, 3); err != nil {
		t.Fatalf("FilteredProductsPage: %v", err)
	}
	want := "/www/account/getFilteredProducts?hiddenFlag=1&isUpdated=1&mediaType=1&sortBy=title&system=&page=3&tags=gog,wishlist"
	if *lastURI != want {
		t.Errorf("request URI = %q, want %q", *lastURI, want)
	}
	if strings.Contains(*lastURI, "%2C") {
		t.Error("tags must not be percent-encoded (D5)")
	}
}

func TestFilteredProductsPageWithoutTagsOmitsParam(t *testing.T) {
	srv, lastURI := productServer(t, `{"page":1,"totalPages":1,"products":[]}`, http.StatusOK)
	cl, _ := newTestClient(t, srv, 0)

	if _, err := cl.FilteredProductsPage(context.Background(), ProductQuery{}, 1); err != nil {
		t.Fatalf("FilteredProductsPage: %v", err)
	}
	if strings.Contains(*lastURI, "tags=") {
		t.Errorf("request URI = %q, want no tags parameter", *lastURI)
	}
}

func TestFilteredProductsPageDecodesProducts(t *testing.T) {
	body := `{"page":2,"totalPages":5,"products":[{"slug":"alpha","id":1},{"slug":"beta","id":"x"}]}`
	srv, _ := productServer(t, body, http.StatusOK)
	cl, _ := newTestClient(t, srv, 0)

	got, err := cl.FilteredProductsPage(context.Background(), ProductQuery{}, 2)
	if err != nil {
		t.Fatalf("FilteredProductsPage: %v", err)
	}
	if got.Page != 2 || got.TotalPages != 5 {
		t.Errorf("page/totalPages = %d/%d, want 2/5", got.Page, got.TotalPages)
	}
	if len(got.Products) != 2 || got.Products[0]["slug"] != "alpha" || got.Products[1]["id"] != "x" {
		t.Errorf("products = %v", got.Products)
	}
}

// TestFilteredProductsPageEmptyListing locks the D3 split: an explicit integer
// totalPages of 0 is a normal empty listing.
func TestFilteredProductsPageEmptyListing(t *testing.T) {
	srv, _ := productServer(t, `{"page":1,"totalPages":0,"products":[]}`, http.StatusOK)
	cl, _ := newTestClient(t, srv, 0)

	got, err := cl.FilteredProductsPage(context.Background(), ProductQuery{}, 1)
	if err != nil {
		t.Fatalf("FilteredProductsPage: %v", err)
	}
	if got.TotalPages != 0 || len(got.Products) != 0 {
		t.Errorf("page = %+v, want an empty listing", got)
	}
}

// TestFilteredProductsPageRejectsBadPageFields locks D3: a missing or
// malformed page/totalPages must not read as "no more results".
func TestFilteredProductsPageRejectsBadPageFields(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"missing both", `{"products":[]}`},
		{"missing totalPages", `{"page":1,"products":[]}`},
		{"missing page", `{"totalPages":1,"products":[]}`},
		{"null page", `{"page":null,"totalPages":1,"products":[]}`},
		{"null totalPages", `{"page":1,"totalPages":null,"products":[]}`},
		{"string page", `{"page":"1","totalPages":1,"products":[]}`},
		{"string totalPages", `{"page":1,"totalPages":"3","products":[]}`},
		{"non-integral page", `{"page":1.5,"totalPages":1,"products":[]}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv, _ := productServer(t, c.body, http.StatusOK)
			cl, _ := newTestClient(t, srv, 0)
			if _, err := cl.FilteredProductsPage(context.Background(), ProductQuery{}, 1); err == nil {
				t.Fatal("FilteredProductsPage: want error for a malformed page field")
			}
		})
	}
}

// TestFilteredProductsPageNotJSON locks ErrNotJSON for shape failures.
func TestFilteredProductsPageNotJSON(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"empty body", ""},
		{"html login page", "<html><body>login</body></html>"},
		{"json array", `[1,2]`},
		{"json scalar", `"nope"`},
		{"truncated json", `{"page":1,`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv, _ := productServer(t, c.body, http.StatusOK)
			cl, _ := newTestClient(t, srv, 0)
			_, err := cl.FilteredProductsPage(context.Background(), ProductQuery{}, 1)
			if !errors.Is(err, ErrNotJSON) {
				t.Errorf("err = %v, want ErrNotJSON", err)
			}
		})
	}
}

// TestFilteredProductsPageProductsShapes: a missing or non-array products field
// yields no products without an error, but a product that is not an object is
// an error.
func TestFilteredProductsPageProductsShapes(t *testing.T) {
	ok := []struct {
		name string
		body string
	}{
		{"missing products", `{"page":1,"totalPages":1}`},
		{"null products", `{"page":1,"totalPages":1,"products":null}`},
		{"string products", `{"page":1,"totalPages":1,"products":"x"}`},
		{"object products", `{"page":1,"totalPages":1,"products":{"a":1}}`},
	}
	for _, c := range ok {
		t.Run(c.name, func(t *testing.T) {
			srv, _ := productServer(t, c.body, http.StatusOK)
			cl, _ := newTestClient(t, srv, 0)
			got, err := cl.FilteredProductsPage(context.Background(), ProductQuery{}, 1)
			if err != nil {
				t.Fatalf("FilteredProductsPage: %v", err)
			}
			if len(got.Products) != 0 {
				t.Errorf("products = %v, want none", got.Products)
			}
		})
	}

	srv, _ := productServer(t, `{"page":1,"totalPages":1,"products":["not-an-object"]}`, http.StatusOK)
	cl, _ := newTestClient(t, srv, 0)
	if _, err := cl.FilteredProductsPage(context.Background(), ProductQuery{}, 1); err == nil {
		t.Error("non-object product: want error")
	}
}

// TestFilteredProductsPageHTTPError: HTTP failures stay StatusError, never
// ErrNotJSON.
func TestFilteredProductsPageHTTPError(t *testing.T) {
	srv, _ := productServer(t, "boom", http.StatusInternalServerError)
	cl, _ := newTestClient(t, srv, 0)

	_, err := cl.FilteredProductsPage(context.Background(), ProductQuery{}, 1)
	var se *httpx.StatusError
	if !errors.As(err, &se) || se.Code != http.StatusInternalServerError {
		t.Fatalf("err = %v, want *httpx.StatusError 500", err)
	}
	if errors.Is(err, ErrNotJSON) {
		t.Error("HTTP error must not be reported as ErrNotJSON")
	}
}

func TestWishlistPageRequestAndDecode(t *testing.T) {
	body := `{"page":1,"totalPages":2,"products":[{"title":"Wanted"}]}`
	srv, lastURI := productServer(t, body, http.StatusOK)
	cl, _ := newTestClient(t, srv, 0)

	got, err := cl.WishlistPage(context.Background(), 4)
	if err != nil {
		t.Fatalf("WishlistPage: %v", err)
	}
	want := "/www/account/wishlist/search?hasHiddenProducts=false&hiddenFlag=0&isUpdated=0&mediaType=0&sortBy=title&system=&page=4"
	if *lastURI != want {
		t.Errorf("request URI = %q, want %q", *lastURI, want)
	}
	if len(got.Products) != 1 || got.Products[0]["title"] != "Wanted" {
		t.Errorf("products = %v", got.Products)
	}
}
