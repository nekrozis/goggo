package catalog

import (
	"context"
	"errors"
	"maps"
	"testing"

	"github.com/nekrozis/goggo/internal/config"
	"github.com/nekrozis/goggo/internal/model"
	"github.com/nekrozis/goggo/internal/webapi"
)

// fakeWishlist is a scripted WishlistFetcher.
type fakeWishlist struct {
	pages []webapi.ProductPage
	calls []int
	err   error
}

func (f *fakeWishlist) WishlistPage(_ context.Context, page int) (webapi.ProductPage, error) {
	f.calls = append(f.calls, page)
	if f.err != nil {
		return webapi.ProductPage{}, f.err
	}
	if len(f.pages) == 0 {
		return webapi.ProductPage{Page: page, TotalPages: page}, nil
	}
	pg := f.pages[0]
	f.pages = f.pages[1:]
	return pg, nil
}

// wishProduct builds a raw wishlist product; price members are merged into the
// "price" object.
func wishProduct(extra map[string]any, price map[string]any) map[string]any {
	p := map[string]any{}
	maps.Copy(p, extra)
	priceObj := map[string]any{}
	maps.Copy(priceObj, price)
	p["price"] = priceObj
	return p
}

func wishlist(t *testing.T, fw *fakeWishlist, opts WishlistOptions) []model.WishlistItem {
	t.Helper()
	items, err := Wishlist(context.Background(), fw, opts)
	if err != nil {
		t.Fatalf("Wishlist: %v", err)
	}
	return items
}

func TestWishlistMapsFields(t *testing.T) {
	fw := &fakeWishlist{pages: []webapi.ProductPage{{Page: 1, TotalPages: 1, Products: []map[string]any{
		wishProduct(map[string]any{
			"title":        "Wanted",
			"isComingSoon": false,
			"isDiscounted": true,
			"isMovie":      false,
			"url":          "/game/wanted",
			"worksOn":      map[string]any{"Linux": true},
		}, map[string]any{
			"symbol":                     "$",
			"finalAmount":                12.5,
			"discountPercentage":         50,
			"discountDifference":         "1.25",
			"bonusStoreCreditAmount":     0,
			"isBonusStoreCreditIncluded": true,
		}),
	}}}}

	items := wishlist(t, fw, WishlistOptions{})
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	got := items[0]
	if got.Title != "Wanted" || got.Currency != "$" {
		t.Errorf("title/currency = %q/%q", got.Title, got.Currency)
	}
	if got.Price != "12.500000$" {
		t.Errorf("price = %q, want 12.500000$", got.Price)
	}
	if got.DiscountPercent != "50%" {
		t.Errorf("discount percent = %q", got.DiscountPercent)
	}
	if got.Discount != "1.25$" {
		t.Errorf("discount = %q (string amount is kept verbatim)", got.Discount)
	}
	if got.StoreCredit != "0.000000$" {
		t.Errorf("store credit = %q (integer amount takes the six-decimal form)", got.StoreCredit)
	}
	if got.URL != "https://www.gog.com/game/wanted" {
		t.Errorf("url = %q", got.URL)
	}
	if got.Platform != config.PlatformLinux {
		t.Errorf("platform = %d", got.Platform)
	}
	if !got.IsDiscounted || !got.IsBonusStoreCreditIncluded {
		t.Errorf("flags = %+v", got)
	}
	if len(got.Tags) != 1 || got.Tags[0] != "Discount" {
		t.Errorf("tags = %v", got.Tags)
	}
}

// TestWishlistAmountShapes locks the isDouble gate: integers take the
// six-decimal form, strings stay verbatim, null/missing become empty.
func TestWishlistAmountShapes(t *testing.T) {
	cases := []struct {
		name string
		set  bool
		in   any
		want string
	}{
		{"absent", false, nil, ""},
		{"null", true, nil, ""},
		{"integer zero", true, float64(0), "0.000000"},
		{"integer", true, float64(7), "7.000000"},
		{"real", true, 12.34, "12.340000"},
		{"numeric string", true, "12.34", "12.34"},
		{"boolean", true, true, "true"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			price := map[string]any{"symbol": "$"}
			if c.set {
				price["finalAmount"] = c.in
			}
			fw := &fakeWishlist{pages: []webapi.ProductPage{{Page: 1, TotalPages: 1,
				Products: []map[string]any{wishProduct(nil, price)}}}}
			if got := wishlist(t, fw, WishlistOptions{})[0].Price; got != c.want+"$" {
				t.Errorf("price = %q, want %q", got, c.want+"$")
			}
		})
	}
}

// TestWishlistPercentShapes locks the separate isInt gate.
func TestWishlistPercentShapes(t *testing.T) {
	cases := []struct {
		name string
		set  bool
		in   any
		want string
	}{
		{"absent", false, nil, "%"},
		{"null", true, nil, "%"},
		{"integer", true, float64(50), "50%"},
		{"integral real", true, 50.0, "50%"},
		{"real", true, 50.5, "50.5%"},
		{"numeric string", true, "50", "50%"},
		{"boolean", true, true, "true%"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			price := map[string]any{"symbol": "$"}
			if c.set {
				price["discountPercentage"] = c.in
			}
			fw := &fakeWishlist{pages: []webapi.ProductPage{{Page: 1, TotalPages: 1,
				Products: []map[string]any{wishProduct(nil, price)}}}}
			if got := wishlist(t, fw, WishlistOptions{})[0].DiscountPercent; got != c.want {
				t.Errorf("discount percent = %q, want %q", got, c.want)
			}
		})
	}
}

func TestWishlistTagOrder(t *testing.T) {
	cases := []struct {
		name                          string
		comingSoon, discounted, movie bool
		want                          []string
	}{
		{"plain", false, false, false, nil},
		{"coming soon", true, false, false, []string{"Coming soon"}},
		{"discount", false, true, false, []string{"Discount"}},
		{"movie", false, false, true, []string{"Movie"}},
		{"all three", true, true, true, []string{"Coming soon", "Discount", "Movie"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fw := &fakeWishlist{pages: []webapi.ProductPage{{Page: 1, TotalPages: 1, Products: []map[string]any{
				wishProduct(map[string]any{
					"isComingSoon": c.comingSoon, "isDiscounted": c.discounted, "isMovie": c.movie,
				}, nil),
			}}}}
			got := wishlist(t, fw, WishlistOptions{})[0].Tags
			if len(got) != len(c.want) {
				t.Fatalf("tags = %v, want %v", got, c.want)
			}
			for i := range c.want {
				if got[i] != c.want[i] {
					t.Errorf("tags[%d] = %q, want %q", i, got[i], c.want[i])
				}
			}
		})
	}
}

// TestWishlistMoviesSkipPlatformDetection: a movie has no platform and must not
// be filtered by the platform check.
func TestWishlistMoviesSkipPlatformDetection(t *testing.T) {
	opts := WishlistOptions{PlatformDetection: true, InstallerPlatform: config.PlatformLinux}
	fw := &fakeWishlist{pages: []webapi.ProductPage{{Page: 1, TotalPages: 1, Products: []map[string]any{
		wishProduct(map[string]any{"title": "movie", "isMovie": true}, nil),
		wishProduct(map[string]any{"title": "windows game", "isMovie": false,
			"worksOn": map[string]any{"Windows": true}}, nil),
		wishProduct(map[string]any{"title": "linux game", "isMovie": false,
			"worksOn": map[string]any{"Linux": true}}, nil),
	}}}}

	items := wishlist(t, fw, opts)
	if len(items) != 2 || items[0].Title != "movie" || items[1].Title != "linux game" {
		t.Fatalf("items = %+v", items)
	}
	if items[0].Platform != 0 {
		t.Errorf("movie platform = %d, want 0", items[0].Platform)
	}
}

// TestWishlistReleaseDate covers
func TestWishlistReleaseDate(t *testing.T) {
	cases := []struct {
		name       string
		comingSoon bool
		set        bool
		in         any
		want       int64
	}{
		{"not coming soon", false, true, float64(123), 0},
		{"absent", true, false, nil, 0},
		{"null", true, true, nil, 0},
		{"empty array", true, true, []any{}, 0},
		{"empty object", true, true, map[string]any{}, 0},
		{"zero", true, true, float64(0), 0},
		{"integer", true, true, float64(1234567890), 1234567890},
		{"real", true, true, 12.9, 12},
		{"numeric string", true, true, "123", 123},
		{"string with suffix", true, true, "123abc", 123},
		{"not a number", true, true, "abc", 0},
		{"empty string", true, true, "", 0},
		{"boolean", true, true, true, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			extra := map[string]any{"isComingSoon": c.comingSoon}
			if c.set {
				extra["releaseDate"] = c.in
			}
			fw := &fakeWishlist{pages: []webapi.ProductPage{{Page: 1, TotalPages: 1,
				Products: []map[string]any{wishProduct(extra, nil)}}}}
			if got := wishlist(t, fw, WishlistOptions{})[0].ReleaseDateTime; got != c.want {
				t.Errorf("release = %d, want %d", got, c.want)
			}
		})
	}
}

// TestWishlistURLs covers
// behaviour for an empty URL.
func TestWishlistURLs(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{"https://example.com/x", "https://example.com/x"},
		{"http://example.com/x", "http://example.com/x"},
		{"httpfoo", "httpfoo"}, // a plain "http" prefix is enough
		{"/game/x", "https://www.gog.com/game/x"},
		{"game/x", "https://www.gog.com/game/x"},
		{"", "https://www.gog.com/"}, // front() on an empty string: pinned libstdc++ result
		{nil, "https://www.gog.com/"},
	}
	for _, c := range cases {
		t.Run(c.want, func(t *testing.T) {
			fw := &fakeWishlist{pages: []webapi.ProductPage{{Page: 1, TotalPages: 1,
				Products: []map[string]any{wishProduct(map[string]any{"url": c.in}, nil)}}}}
			if got := wishlist(t, fw, WishlistOptions{})[0].URL; got != c.want {
				t.Errorf("url = %q, want %q", got, c.want)
			}
		})
	}
}

// TestWishlistPagination locks the `page >= totalPages` termination and that
// pages are merged in order.
func TestWishlistPagination(t *testing.T) {
	fw := &fakeWishlist{pages: []webapi.ProductPage{
		{Page: 1, TotalPages: 2, Products: []map[string]any{wishProduct(map[string]any{"title": "one"}, nil)}},
		{Page: 2, TotalPages: 2, Products: []map[string]any{wishProduct(map[string]any{"title": "two"}, nil)}},
	}}
	items := wishlist(t, fw, WishlistOptions{})
	if len(items) != 2 || items[0].Title != "one" || items[1].Title != "two" {
		t.Fatalf("items = %+v", items)
	}
	if len(fw.calls) != 2 || fw.calls[0] != 1 || fw.calls[1] != 2 {
		t.Errorf("page calls = %v, want [1 2]", fw.calls)
	}

	// totalPages == 0 still terminates after the first page (1 >= 0).
	zero := &fakeWishlist{pages: []webapi.ProductPage{{Page: 1, TotalPages: 0}}}
	if got := wishlist(t, zero, WishlistOptions{}); len(got) != 0 {
		t.Errorf("items = %+v, want none", got)
	}
	if len(zero.calls) != 1 {
		t.Errorf("page calls = %v, want one", zero.calls)
	}

	// A page already at the end stops immediately.
	ahead := &fakeWishlist{pages: []webapi.ProductPage{{Page: 2, TotalPages: 1}}}
	wishlist(t, ahead, WishlistOptions{})
	if len(ahead.calls) != 1 {
		t.Errorf("page calls = %v, want one", ahead.calls)
	}
}

func TestWishlistFetcherError(t *testing.T) {
	boom := errors.New("boom")
	fw := &fakeWishlist{err: boom}
	if _, err := Wishlist(context.Background(), fw, WishlistOptions{}); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
}

// TestWishlistMissingPriceObject: a price member that is not an object leaves the
// fields at their empty forms.
func TestWishlistMissingPriceObject(t *testing.T) {
	fw := &fakeWishlist{pages: []webapi.ProductPage{{Page: 1, TotalPages: 1, Products: []map[string]any{
		{"title": "no price", "price": "oops"},
	}}}}
	got := wishlist(t, fw, WishlistOptions{})[0]
	if got.Title != "no price" {
		t.Errorf("title = %q", got.Title)
	}
	if got.Currency != "" || got.Price != "" || got.DiscountPercent != "%" || got.Discount != "" || got.StoreCredit != "" {
		t.Errorf("price fields = %+v", got)
	}
}

// TestAmountStringContract locks amountString numeric fixed-point formatting,
// non-numeric scalar fallback, and container rejection.
func TestAmountStringContract(t *testing.T) {
	cases := []struct {
		name    string
		in      any
		want    string
		wantErr bool
	}{
		{name: "float64 decimal", in: float64(1.25), want: "1.250000"},
		{name: "float64 zero", in: float64(0), want: "0.000000"},
		{name: "int", in: int(5), want: "5.000000"},
		{name: "int64", in: int64(123), want: "123.000000"},
		{name: "uint64", in: uint64(999), want: "999.000000"},
		{name: "string number verbatim", in: "1.25", want: "1.25"},
		{name: "string arbitrary", in: "free", want: "free"},
		{name: "bool true", in: true, want: "true"},
		{name: "bool false", in: false, want: "false"},
		{name: "null empty", in: nil, want: ""},
		{name: "array rejected", in: []any{1.25}, wantErr: true},
		{name: "object rejected", in: map[string]any{"amt": 1.25}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := amountString(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("amountString(%#v) expected error, got %q", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("amountString(%#v) unexpected error: %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("amountString(%#v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
