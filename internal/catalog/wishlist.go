package catalog

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/nekrozis/goggo/internal/jsonval"
	"github.com/nekrozis/goggo/internal/model"
	"github.com/nekrozis/goggo/internal/webapi"
)

// WishlistFetcher is the slice of the website client the wishlist listing
// needs. It is satisfied by *webapi.Client; the formatting stays here, so
// webapi keeps returning raw pages only.
type WishlistFetcher interface {
	WishlistPage(ctx context.Context, page int) (webapi.ProductPage, error)
}

// WishlistOptions mirrors the two configuration values Website::getWishlistItems
// reads (website.cpp:733): the listing has no tag, newness or hidden-product
// knobs.
//
// Fields are ordered to minimise padding: the uint32 first, then the bool.
type WishlistOptions struct {
	// InstallerPlatform is the platform mask a product must support when
	// PlatformDetection is on.
	InstallerPlatform uint32

	// PlatformDetection skips non-movie products that do not support
	// InstallerPlatform (movies are never platform-checked).
	PlatformDetection bool
}

// Wishlist runs the wishlist listing (website.cpp:696-797).
//
// Pagination ends when the response reports `page >= totalPages`; unlike the
// product listing there is no hidden-products pass, no sorting and no DLC
// enrichment.
func Wishlist(ctx context.Context, wx WishlistFetcher, opts WishlistOptions) ([]model.WishlistItem, error) {
	items := []model.WishlistItem{}
	page := 1
	for {
		pg, err := wx.WishlistPage(ctx, page)
		if err != nil {
			return nil, err
		}
		parsed := pg.Page >= pg.TotalPages
		for _, product := range pg.Products {
			item, skip, err := mapWishlistItem(product, opts)
			if err != nil {
				return nil, err
			}
			if !skip {
				items = append(items, item)
			}
		}
		page++
		if parsed {
			break
		}
	}
	return items, nil
}

// mapWishlistItem maps one raw product. skip reports that the platform filter
// excluded the entry (website.cpp:732-734).
func mapWishlistItem(p map[string]any, opts WishlistOptions) (model.WishlistItem, bool, error) {
	var item model.WishlistItem

	isMovie, err := jsonval.Bool(p["isMovie"])
	if err != nil {
		return item, false, fmt.Errorf("catalog: isMovie: %w", err)
	}
	if !isMovie {
		// Movies never carry a platform and are never platform-filtered.
		bits, err := wishlistPlatformBits(p["worksOn"])
		if err != nil {
			return item, false, err
		}
		item.Platform = bits
		if opts.PlatformDetection && bits&opts.InstallerPlatform == 0 {
			return model.WishlistItem{}, true, nil
		}
	}

	comingSoon, err := jsonval.Bool(p["isComingSoon"])
	if err != nil {
		return item, false, fmt.Errorf("catalog: isComingSoon: %w", err)
	}
	discounted, err := jsonval.Bool(p["isDiscounted"])
	if err != nil {
		return item, false, fmt.Errorf("catalog: isDiscounted: %w", err)
	}
	// Tag order follows the push order of website.cpp:737-742.
	if comingSoon {
		item.Tags = append(item.Tags, "Coming soon")
	}
	if discounted {
		item.Tags = append(item.Tags, "Discount")
	}
	if isMovie {
		item.Tags = append(item.Tags, "Movie")
	}

	if item.ReleaseDateTime, err = wishlistReleaseDate(p, comingSoon); err != nil {
		return item, false, err
	}

	// A price member that is absent or not an object reads as null in jsoncpp,
	// so every field below degrades to its empty form instead of failing.
	price := asObject(p["price"])
	if item.Currency, err = stringField(price, "symbol", "currency"); err != nil {
		return item, false, err
	}
	if item.Price, err = amountField(price, "finalAmount"); err != nil {
		return item, false, err
	}
	item.Price += item.Currency
	if item.DiscountPercent, err = percentField(price, "discountPercentage"); err != nil {
		return item, false, err
	}
	if item.Discount, err = amountField(price, "discountDifference"); err != nil {
		return item, false, err
	}
	item.Discount += item.Currency
	if item.StoreCredit, err = amountField(price, "bonusStoreCreditAmount"); err != nil {
		return item, false, err
	}
	item.StoreCredit += item.Currency

	if item.Title, err = stringField(p, "title", "title"); err != nil {
		return item, false, err
	}
	if item.URL, err = wishlistURL(p["url"]); err != nil {
		return item, false, err
	}
	if item.IsBonusStoreCreditIncluded, err = boolField(price, "isBonusStoreCreditIncluded"); err != nil {
		return item, false, err
	}
	item.IsDiscounted = discounted
	return item, false, nil
}

// wishlistPlatformBits derives the worksOn mask without the "no platform means
// all platforms" fallback the product listing applies (website.cpp:725-730).
func wishlistPlatformBits(worksOn any) (uint32, error) {
	obj, ok := worksOn.(map[string]any)
	if !ok {
		return 0, nil
	}
	return platformBits(obj)
}

// wishlistReleaseDate mirrors website.cpp:744-769: the date is read only for a
// coming-soon product, an empty value is skipped, an integer-shaped value is
// taken as-is and anything else goes through std::stoi on its string form.
func wishlistReleaseDate(p map[string]any, comingSoon bool) (int64, error) {
	if !comingSoon {
		return 0, nil
	}
	v, ok := p["releaseDate"]
	if !ok || isEmptyJSON(v) {
		return 0, nil
	}
	switch v.(type) {
	case int, int64, float64:
		if n, err := jsonval.Int(v); err == nil {
			return n, nil
		}
		// A non-integral number takes the string path, exactly as isInt()
		// being false does in jsoncpp.
	}
	s, err := jsonval.Str(v)
	if err != nil {
		return 0, fmt.Errorf("catalog: releaseDate: %w", err)
	}
	return int64(atoiPrefix(s)), nil
}

// isEmptyJSON mirrors jsoncpp's Value::empty(): true for null, an empty array
// and an empty object. An empty string, 0 and false are NOT empty, so they
// still reach the parsing path (which yields 0 for them).
func isEmptyJSON(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case []any:
		return len(t) == 0
	case map[string]any:
		return len(t) == 0
	default:
		return false
	}
}

// wishlistURL mirrors website.cpp:777-784: a URL already starting with "http"
// is kept as-is, one starting with "/" gets the host prefix, and anything else
// is appended to host+"/".
//
// An empty URL reaches the last branch. The C++ source calls std::string::front()
// there, which is undefined behaviour on an empty string; the observed result on
// libstdc++ is '\0', which is not '/', so the else branch runs. This records that
// observed outcome — it does not claim the standard defines it.
func wishlistURL(v any) (string, error) {
	raw, err := jsonval.Str(v)
	if err != nil {
		return "", fmt.Errorf("catalog: url: %w", err)
	}
	switch {
	case strings.HasPrefix(raw, "http"):
		return raw, nil
	case strings.HasPrefix(raw, "/"):
		return webapi.DefaultWWWHost + raw, nil
	default:
		return webapi.DefaultWWWHost + "/" + raw, nil
	}
}

// asObject returns v as a map, or nil when it is absent or not an object.
// Indexing a nil map yields the zero value, which is how the port represents
// jsoncpp returning null for a member of a non-object value.
func asObject(v any) map[string]any {
	if obj, ok := v.(map[string]any); ok {
		return obj
	}
	return nil
}

// stringField reads a string-valued member, reporting context on error.
func stringField(obj map[string]any, key, field string) (string, error) {
	s, err := jsonval.Str(obj[key])
	if err != nil {
		return "", fmt.Errorf("catalog: %s: %w", field, err)
	}
	return s, nil
}

// boolField reads a boolean-valued member, reporting context on error.
func boolField(obj map[string]any, key string) (bool, error) {
	b, err := jsonval.Bool(obj[key])
	if err != nil {
		return false, fmt.Errorf("catalog: %s: %w", key, err)
	}
	return b, nil
}

// amountField renders a price member with the `isDouble()` rule.
func amountField(obj map[string]any, key string) (string, error) {
	s, err := amountString(obj[key])
	if err != nil {
		return "", fmt.Errorf("catalog: %s: %w", key, err)
	}
	return s, nil
}

// percentField renders the discount percentage with the `isInt()` rule.
func percentField(obj map[string]any, key string) (string, error) {
	s, err := intShapedString(obj[key])
	if err != nil {
		return "", fmt.Errorf("catalog: %s: %w", key, err)
	}
	return s + "%", nil
}

// amountString mirrors `isDouble() ? std::to_string(asDouble()) : asString()`
// (website.cpp:772-775). jsoncpp's isDouble() is true for integer values as
// well, and jsonval.IsNumber is the exact Go predicate for that set, so an
// integer amount becomes "0.000000" rather than "0".
func amountString(v any) (string, error) {
	if jsonval.IsNumber(v) {
		f, err := jsonval.Num(v)
		if err != nil {
			return "", err
		}
		// std::to_string(double) prints a fixed six decimals, never an
		// exponent; FormatFloat with 'f' and precision 6 matches it.
		return strconv.FormatFloat(f, 'f', 6, 64), nil
	}
	return jsonval.Str(v)
}
