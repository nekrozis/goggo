package model

// WishlistItem is one entry of the account wishlist, with the display strings
// pre-formatted for the listing.
type WishlistItem struct {
	// Title is the product title; Currency and the four price strings that
	// follow are pre-formatted (Price, Discount and StoreCredit already carry
	// the currency symbol, DiscountPercent carries "%"), and URL is the
	// absolute product page.
	Title           string `json:"title"`
	Currency        string `json:"currency"`
	Price           string `json:"price"`
	DiscountPercent string `json:"discountPercent"`
	Discount        string `json:"discount"`
	StoreCredit     string `json:"storeCredit"`
	URL             string `json:"url"`
	// Tags holds the display tags: "Coming soon", "Discount", "Movie".
	Tags []string `json:"tags,omitempty"`

	// ReleaseDateTime is the release date as a Unix timestamp, 0 when unknown.
	ReleaseDateTime int64 `json:"releaseDateTime"`

	// Platform is the worksOn mask; it stays 0 for movies.
	Platform uint32 `json:"platform"`

	// IsBonusStoreCreditIncluded and IsDiscounted carry the two flags the
	// listing needs.
	IsBonusStoreCreditIncluded bool `json:"isBonusStoreCreditIncluded"`
	IsDiscounted               bool `json:"isDiscounted"`
}
