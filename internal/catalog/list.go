// Package catalog assembles the account game list from the website listings:
// it drives the paginated product fetches, maps each product to a
// model.GameItem, applies the name/platform/new filters and optionally
// enriches entries with their DLC names.
//
// It ports Website::getGames (website.cpp:92-303). The HTTP primitives stay in
// internal/webapi; this package holds the business filtering, so that the
// transport layer never learns about --game-regex, --new or --include.
//
// Not ported here (gap, S23): the per-game configuration file override of
// bIgnoreDLCCount (Util::getGameSpecificConfig). The C++ source skips that
// lookup entirely during a cache update, so the --update-cache path is
// unaffected until S23.
package catalog

import (
	"context"
	"fmt"
	"sort"
	"strconv"

	"github.com/nekrozis/goggo/internal/config"
	"github.com/nekrozis/goggo/internal/jsonval"
	"github.com/nekrozis/goggo/internal/model"
	"github.com/nekrozis/goggo/internal/util"
	"github.com/nekrozis/goggo/internal/webapi"
)

// ProductFetcher is the slice of the website client the listing needs. It is
// satisfied by *webapi.Client; tests provide their own implementation.
type ProductFetcher interface {
	FilteredProductsPage(ctx context.Context, q webapi.ProductQuery, page int) (webapi.ProductPage, error)
	GameDetailsJSON(ctx context.Context, gameID string) (map[string]any, error)
	OwnedGameIDs(ctx context.Context) ([]string, error)
}

// ListOptions mirrors the configuration values Website::getGames reads from
// the global config (website.cpp:98-166,217-292).
//
// Fields are ordered to minimise padding: the slice (24B), the strings (16B
// each), the uint32 pair (4B each), then the bools.
type ListOptions struct {
	// Tags is forwarded to the products query, already split on commas.
	Tags []string

	// GameRegex is --game-regex; FilterListPath is --game-list-file (used only
	// when GameRegex is empty).
	GameRegex      string
	FilterListPath string

	// IgnoreDLCCountRE is --ignore-dlc-count-regex.
	IgnoreDLCCountRE string

	// InstallerPlatform is the platform mask the product must support when
	// PlatformDetection is on.
	InstallerPlatform uint32

	// Include is the --include mask; DLC information is only fetched when it
	// contains config.GFDLC.
	Include uint32

	// Updated maps to --updated (isUpdated query parameter).
	Updated bool

	// NewOnly skips products that are not marked as new.
	NewOnly bool

	// IncludeHidden runs a second pass over hidden products and sorts the
	// result by name.
	IncludeHidden bool

	// PlatformDetection skips products that do not support InstallerPlatform.
	PlatformDetection bool

	// UpdateCache disables per-game configuration files (S23).
	UpdateCache bool
}

// ListResult is the outcome of a listing run.
//
// OwnedIDs carries the owned product ids the C++ getGames() fetched into the
// process-wide Globals::vOwnedGamesIds. Returning them keeps the same fetch
// moment without reintroducing global state (review lock, D2); the caller
// (S13) decides what to do with them.
//
// Fields are ordered to minimise padding: the slices (24B each).
type ListResult struct {
	Games    []model.GameItem
	OwnedIDs []string
}

// List runs the full listing: it compiles the filters, fetches the owned ids
// and the product pages, then maps and filters the products.
//
// Errors are propagated rather than swallowed: in particular a failing
// OwnedGameIDs call fails the whole listing instead of pretending the account
// owns nothing (review lock, E1 — a deliberate departure from the C++
// getResponseJson behaviour of returning an empty value on error).
func List(ctx context.Context, wx ProductFetcher, opts ListOptions) (ListResult, error) {
	filters, err := CompileFilters(opts.GameRegex, opts.FilterListPath, opts.IgnoreDLCCountRE)
	if err != nil {
		return ListResult{}, err
	}
	owned, err := wx.OwnedGameIDs(ctx)
	if err != nil {
		return ListResult{}, err
	}
	products, err := fetchProducts(ctx, wx, opts)
	if err != nil {
		return ListResult{}, err
	}

	games := make([]model.GameItem, 0, len(products))
	for _, product := range products {
		item, platform, err := mapProduct(product)
		if err != nil {
			return ListResult{}, err
		}
		// Filter order mirrors the original: newness, then platform, then the
		// name regex list (website.cpp:217-242).
		if opts.NewOnly && !item.IsNew {
			continue
		}
		if opts.PlatformDetection && platform&opts.InstallerPlatform == 0 {
			continue
		}
		if len(filters.Games) > 0 && !MatchesAny(filters.Games, item.Name) {
			continue
		}
		if opts.Include&config.GFDLC != 0 {
			if err := enrichDLC(ctx, wx, product, filters, &item); err != nil {
				return ListResult{}, err
			}
		}
		games = append(games, item)
	}

	if opts.IncludeHidden {
		// std::sort by name (website.cpp:299); equal names stay unspecified in
		// both implementations.
		sort.Slice(games, func(i, j int) bool { return games[i].Name < games[j].Name })
	}
	return ListResult{Games: games, OwnedIDs: owned}, nil
}

// fetchProducts walks the product pages, including the hidden-products pass
// when requested (website.cpp:111-140).
//
// The C++ loop ends on `page == totalPages || totalPages == 0`; that test is
// evaluated on the decoded page, and a page that is missing those fields is an
// error in webapi (S11a, D3), so a malformed response can no longer end the
// walk silently.
func fetchProducts(ctx context.Context, wx ProductFetcher, opts ListOptions) ([]map[string]any, error) {
	var products []map[string]any
	hidden := 0
	page := 1
	isUpdated := 0
	if opts.Updated {
		isUpdated = 1
	}
	for {
		q := webapi.ProductQuery{Tags: opts.Tags, HiddenFlag: hidden, IsUpdated: isUpdated}
		pg, err := wx.FilteredProductsPage(ctx, q, page)
		if err != nil {
			return nil, err
		}
		parsed := pg.Page == pg.TotalPages || pg.TotalPages == 0
		if opts.IncludeHidden && parsed && hidden == 0 {
			// Make the next iteration handle hidden products; page is reset to
			// 0 because the increment below moves it to 1 (website.cpp:126-132).
			parsed = false
			hidden = 1
			page = 0
		}
		products = append(products, pg.Products...)
		page++
		if parsed {
			break
		}
	}
	return products, nil
}

// enrichDLC fills in the game details and DLC names when the product warrants
// it (website.cpp:244-292).
//
// The C++ source additionally re-checks the game regex here; that check is
// always satisfied when it is reached (filters is non-empty and already
// matched, and it is built from the same --game-regex), so it is omitted
// instead of kept as dead code.
//
// A failing details request is skipped: the game stays listed without DLC
// information, mirroring the C++ behaviour of treating an empty response as
// "no details". No counter is kept yet — no consumer needs one.
func enrichDLC(ctx context.Context, wx ProductFetcher, product map[string]any, filters Filters, item *model.GameItem) error {
	dlcCount, err := jsonval.Int(product["dlcCount"])
	if err != nil {
		return fmt.Errorf("catalog: dlcCount: %w", err)
	}
	want := dlcCount != 0
	if !want && filters.IgnoreDLCCount != nil && filters.IgnoreDLCCount.MatchString(item.Name) {
		want = true
	}
	if !want {
		return nil
	}
	details, err := wx.GameDetailsJSON(ctx, item.ID)
	if err != nil {
		return nil // soft skip (review lock, C)
	}
	names, err := util.DLCNamesFromJSON(details["dlcs"])
	if err != nil {
		return nil // soft skip: the details document is unusable, the game is not
	}
	item.GameDetailsJSON = details
	item.DLCNames = names
	return nil
}

// mapProduct turns one raw product into a GameItem plus its platform mask
// (website.cpp:174-214).
func mapProduct(p map[string]any) (model.GameItem, uint32, error) {
	name, err := jsonval.Str(p["slug"])
	if err != nil {
		return model.GameItem{}, 0, fmt.Errorf("catalog: slug: %w", err)
	}
	id, err := productID(p["id"])
	if err != nil {
		return model.GameItem{}, 0, fmt.Errorf("catalog: id: %w", err)
	}
	isNew, err := jsonval.Bool(p["isNew"])
	if err != nil {
		return model.GameItem{}, 0, fmt.Errorf("catalog: isNew: %w", err)
	}
	updates, err := productUpdates(p["updates"])
	if err != nil {
		return model.GameItem{}, 0, err
	}
	platform, err := productPlatform(p["worksOn"])
	if err != nil {
		return model.GameItem{}, 0, err
	}
	return model.GameItem{Name: name, ID: id, Updates: updates, IsNew: isNew}, platform, nil
}

// productID mirrors `product["id"].isInt() ? to_string(asInt()) : asString()`
// (website.cpp:176): integer-shaped ids become decimal text, everything else is
// stringified. A missing id reads as "" (jsoncpp's asString on null) rather
// than "0".
func productID(v any) (string, error) {
	return intShapedString(v)
}

// intShapedString renders a value with the `isInt() ? to_string(asInt()) :
// asString()` rule the C++ source uses for product ids (website.cpp:176) and for
// the wishlist discount percentage (website.cpp:773).
//
// Only integer-shaped values enter the integer branch: a boolean, a string or
// null must stringify instead, because jsoncpp's isInt() is false for them. Do
// not widen this gate — jsonval.Int alone would coerce true to "1".
//
// Boundary note: jsoncpp's isInt() additionally requires the value to fit in
// int32, while jsonval.Int accepts the whole int64 range. Only a value beyond
// 2^31 (or a real whose text form uses an exponent) can differ, which neither a
// product id nor a percentage reaches.
func intShapedString(v any) (string, error) {
	switch v.(type) {
	case int, int64, uint64, float64:
		if n, err := jsonval.Int(v); err == nil {
			return strconv.FormatInt(n, 10), nil
		}
		// A non-integral number is not isInt() in jsoncpp either; it falls
		// through to the string form below.
	}
	return jsonval.Str(v)
}

// productUpdates mirrors website.cpp:179-202: an absent or null member leaves
// the count at 0, otherwise the value goes through std::stoi semantics on its
// string form (leading whitespace skipped, leading digits taken, no digits
// means 0).
//
// Hardening: std::stoi throws out_of_range for an over-long number and the C++
// source only catches invalid_argument, so a huge value would terminate the
// process; here it yields 0.
func productUpdates(v any) (int, error) {
	switch v.(type) {
	case nil:
		return 0, nil
	case int:
		return v.(int), nil
	case int64:
		return int(v.(int64)), nil
	}
	s, err := jsonval.Str(v)
	if err != nil {
		return 0, fmt.Errorf("catalog: updates: %w", err)
	}
	return atoiPrefix(s), nil
}

// atoiPrefix parses the integer prefix of s, with the same acceptance as
// std::stoi: optional leading whitespace, an optional sign, then digits.
// Anything else (including an empty string) yields 0.
func atoiPrefix(s string) int {
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r' || s[i] == '\v' || s[i] == '\f') {
		i++
	}
	start := i
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	digits := i
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == digits {
		return 0
	}
	n, err := strconv.Atoi(s[start:i])
	if err != nil {
		return 0 // out of range: see the hardening note above
	}
	return n
}

// productPlatform derives the platform mask from worksOn
// (website.cpp:204-214). When the product reports no platform at all the mask
// becomes "all platforms", matching Util::getOptionValue("all", PLATFORMS).
func productPlatform(worksOn any) (uint32, error) {
	obj, ok := worksOn.(map[string]any)
	if !ok {
		// A missing (or non-object) worksOn reports nothing, exactly like
		// indexing a null Json::Value.
		return util.OptionValue("all", config.Platforms, false), nil
	}
	platform, err := platformBits(obj)
	if err != nil {
		return 0, err
	}
	if platform == 0 {
		platform = util.OptionValue("all", config.Platforms, false)
	}
	return platform, nil
}

// platformBits maps a worksOn object to its platform mask. The wishlist
// listing uses it without the all-platforms fallback (website.cpp:725-730).
func platformBits(worksOn map[string]any) (uint32, error) {
	var platform uint32
	for _, entry := range []struct {
		key  string
		flag uint32
	}{
		{"Windows", config.PlatformWindows},
		{"Mac", config.PlatformMac},
		{"Linux", config.PlatformLinux},
	} {
		on, err := jsonval.Bool(worksOn[entry.key])
		if err != nil {
			return 0, fmt.Errorf("catalog: worksOn.%s: %w", entry.key, err)
		}
		if on {
			platform |= entry.flag
		}
	}
	return platform, nil
}
