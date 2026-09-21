// Package catalog assembles the account game list from the website listings: it
// drives the paginated product fetches, maps each product to a model.GameItem,
// applies the name/platform/new filters and optionally enriches entries with their
// DLC names.
//
// The HTTP primitives stay in internal/webapi and the business filtering lives here,
// so the transport layer never learns about --game, --new or --include. The
// per-game configuration file override of the DLC-count rule is not implemented here.
package catalog

import (
	"context"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"fmt"
	"math"
	"sort"
	"strconv"

	"github.com/nekrozis/goggo/internal/config"
	"github.com/nekrozis/goggo/internal/model"
	"github.com/nekrozis/goggo/internal/util"
	"github.com/nekrozis/goggo/internal/webapi"
)

// ProductFetcher is the slice of the website client the listing needs. It is
// satisfied by *webapi.Client; tests provide their own implementation.
type ProductFetcher interface {
	FilteredProductsPage(ctx context.Context, q webapi.ProductQuery, page int) (webapi.ProductPage, error)
	GameDetailsJSON(ctx context.Context, gameID string) ([]byte, error)
	OwnedGameIDs(ctx context.Context) ([]string, error)
}

// ListOptions carries the configuration values one listing run reads.
type ListOptions struct {
	// Tags is forwarded to the products query, already split on commas.
	Tags []string

	// GameRegex is --game; FilterListPath is --game-list-file (used only
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

	// UpdateCache disables per-game configuration files.
	UpdateCache bool
}

// ListResult is the outcome of a listing run.
//
// OwnedIDs carries the owned product ids fetched by the same run. Returning
// them keeps the fetch moment without reintroducing global state; the caller
// decides what to do with them.
type ListResult struct {
	Games    []model.GameItem
	OwnedIDs []string
}

// List runs the full listing: it compiles the filters, fetches the owned ids
// and the product pages, then maps and filters the products.
//
// Errors are propagated rather than swallowed: in particular a failing
// OwnedGameIDs call fails the whole listing instead of pretending the account
// owns nothing.
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
		// Filter order: newness, then platform, then the name regex list.
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
		// Sorted by name; the order of equal names is unspecified.
		sort.Slice(games, func(i, j int) bool { return games[i].Name < games[j].Name })
	}
	return ListResult{Games: games, OwnedIDs: owned}, nil
}

// fetchProducts walks the product pages, including the hidden-products pass
// when requested.
//
// The walk ends on `page == totalPages || totalPages == 0`, evaluated on the
// decoded page; a page missing those fields is an error in webapi, so a
// malformed response cannot end the walk silently.
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
			// 0 because the increment below moves it to 1.
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
// it.
//
// A failing details request is skipped: the game stays listed without DLC
// information. No failure counter is kept, because no consumer needs one.
func enrichDLC(ctx context.Context, wx ProductFetcher, product map[string]any, filters Filters, item *model.GameItem) error {
	dlcCount, err := intValue(product["dlcCount"])
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
		return nil // soft skip
	}
	// The walker is handed the subtree it knows how to walk; which member holds
	// that subtree is this consumer's business, not the walker's.
	var doc struct {
		DLCs jsontext.Value `json:"dlcs"`
	}
	if err := jsonv2.Unmarshal(details, &doc); err != nil {
		return nil // soft skip: the details document is unusable, the game is not
	}
	names, err := util.DLCNamesFromJSON(doc.DLCs)
	if err != nil {
		return nil // soft skip: the details document is unusable, the game is not
	}
	item.GameDetailsJSON = details
	item.DLCNames = names
	return nil
}

// mapProduct turns one raw product into a GameItem plus its platform mask.
func mapProduct(p map[string]any) (model.GameItem, uint32, error) {
	name, err := scalarString(p["slug"])
	if err != nil {
		return model.GameItem{}, 0, fmt.Errorf("catalog: slug: %w", err)
	}
	id, err := productID(p["id"])
	if err != nil {
		return model.GameItem{}, 0, fmt.Errorf("catalog: id: %w", err)
	}
	isNew, err := boolValue(p["isNew"])
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

// productID renders the product id: integer-shaped ids become decimal text,
// everything else is stringified. A missing id reads as "" rather than "0".
func productID(v any) (string, error) {
	return intShapedString(v)
}

// intShapedString renders a value with the `isInt ? to_string(asInt): asString` rule
// used for product ids and for the wishlist discount percentage.
//
// Only integer-shaped values enter the integer branch: a boolean, a string or null
// must stringify instead. Do not widen this gate — intValue alone would coerce
// true to "1".
func intShapedString(v any) (string, error) {
	switch v.(type) {
	case int, int64, uint64, float64:
		if n, err := intValue(v); err == nil {
			return strconv.FormatInt(n, 10), nil
		}
		// A non-integral number falls through to the string form below.
	}
	return scalarString(v)
}

// productUpdates reads the update count: an absent or null member leaves it at
// 0, otherwise the value is parsed from its string form with atoiPrefix
// semantics.
//
// An over-long number yields 0 instead of failing the listing.
func productUpdates(v any) (int, error) {
	switch t := v.(type) {
	case nil:
		return 0, nil
	case int:
		return t, nil
	case int64:
		return int(t), nil
	}
	s, err := scalarString(v)
	if err != nil {
		return 0, fmt.Errorf("catalog: updates: %w", err)
	}
	return atoiPrefix(s), nil
}

// atoiPrefix parses the integer prefix of s: optional leading whitespace, an
// optional sign, then digits. Anything else (including an empty string) yields
// 0.
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
		return 0 // out of range: treated as 0
	}
	return n
}

// productPlatform derives the platform mask from worksOn. When the product
// reports no platform at all, the mask becomes all platforms.
func productPlatform(worksOn any) (uint32, error) {
	obj, ok := worksOn.(map[string]any)
	if !ok {
		// A missing (or non-object) worksOn reports no platform.
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
// listing uses it without the all-platforms fallback.
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
		on, err := boolValue(worksOn[entry.key])
		if err != nil {
			return 0, fmt.Errorf("catalog: worksOn.%s: %w", entry.key, err)
		}
		if on {
			platform |= entry.flag
		}
	}
	return platform, nil
}

func mapKind(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case float64, int, int64, uint64:
		return "number"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return fmt.Sprintf("%T", v)
	}
}

func boolValue(v any) (bool, error) {
	switch t := v.(type) {
	case nil:
		return false, nil
	case bool:
		return t, nil
	case int:
		return t != 0, nil
	case int64:
		return t != 0, nil
	case uint64:
		return t != 0, nil
	case float64:
		return t != 0, nil
	default:
		return false, fmt.Errorf("expected a bool, got %s", mapKind(v))
	}
}

const maxInt64Exclusive = float64(1 << 63)

func intValue(v any) (int64, error) {
	switch t := v.(type) {
	case nil:
		return 0, nil
	case bool:
		if t {
			return 1, nil
		}
		return 0, nil
	case int:
		return int64(t), nil
	case int64:
		return t, nil
	case uint64:
		if t > math.MaxInt64 {
			return 0, fmt.Errorf("integer %d overflows int64", t)
		}
		return int64(t), nil
	case float64:
		if math.IsNaN(t) || math.IsInf(t, 0) || t != math.Trunc(t) {
			return 0, fmt.Errorf("expected an integer, got %v", t)
		}
		if t >= maxInt64Exclusive || t < -maxInt64Exclusive {
			return 0, fmt.Errorf("integer %v overflows int64", t)
		}
		return int64(t), nil
	default:
		return 0, fmt.Errorf("expected an integer, got %s", mapKind(v))
	}
}

func scalarString(v any) (string, error) {
	switch t := v.(type) {
	case nil:
		return "", nil
	case string:
		return t, nil
	case bool:
		if t {
			return "true", nil
		}
		return "false", nil
	case int:
		return strconv.Itoa(t), nil
	case int64:
		return strconv.FormatInt(t, 10), nil
	case uint64:
		return strconv.FormatUint(t, 10), nil
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64), nil
	default:
		return "", fmt.Errorf("expected a string, got %s", mapKind(v))
	}
}
