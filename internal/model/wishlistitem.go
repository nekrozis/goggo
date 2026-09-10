package model

// WishlistItem mirrors struct wishlistItem (util.h:45-58): one entry of the
// account wishlist with the display strings the C++ source pre-formats.
//
// Differences from the C++ struct (intentional): the Hungarian/short member
// names are dropped, and the field order follows the project layout rule
// instead of the original declaration order.
//
// Fields are ordered to minimise padding: the slice (24B), the strings (16B
// each), the int64/uint32 pair, then the bools.
type WishlistItem struct {
	// Tags holds the display tags in the order the C++ source pushes them:
	// "Coming soon", "Discount", "Movie".
	Tags []string

	// Title is the product title; Currency and the four price strings that
	// follow are pre-formatted (Price, Discount and StoreCredit already carry
	// the currency symbol, DiscountPercent carries "%"), and URL is the
	// absolute product page.
	Title           string
	Currency        string
	Price           string
	DiscountPercent string
	Discount        string
	StoreCredit     string
	URL             string

	// ReleaseDateTime is the release date as a Unix timestamp, 0 when unknown
	// (website.cpp:744-769).
	ReleaseDateTime int64

	// Platform is the worksOn mask; it stays 0 for movies.
	Platform uint32

	// IsBonusStoreCreditIncluded and IsDiscounted carry the two flags the
	// listing needs.
	IsBonusStoreCreditIncluded bool
	IsDiscounted               bool
}
