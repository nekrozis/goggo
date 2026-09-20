package model

// WishlistItem is one entry of the account wishlist, with the display strings
// pre-formatted for the listing.
type WishlistItem struct {
	// Tags holds the display tags: "Coming soon", "Discount", "Movie".
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

	// ReleaseDateTime is the release date as a Unix timestamp, 0 when unknown.
	ReleaseDateTime int64

	// Platform is the worksOn mask; it stays 0 for movies.
	Platform uint32

	// IsBonusStoreCreditIncluded and IsDiscounted carry the two flags the
	// listing needs.
	IsBonusStoreCreditIncluded bool
	IsDiscounted               bool
}
