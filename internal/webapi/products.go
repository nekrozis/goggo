package webapi

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// ProductQuery is one getFilteredProducts request (website.cpp:113).
//
// Fields are ordered to minimise padding: the slice (24B) first, then the ints
// (8B each).
type ProductQuery struct {
	Tags       []string
	HiddenFlag int
	IsUpdated  int
}

// ProductPage is one decoded page of a product or wishlist listing. Products
// holds the raw product objects so the assembly layer (S11b/S11c) can map them
// without re-fetching.
//
// Fields are ordered to minimise padding: the slice (24B) first, then the ints.
type ProductPage struct {
	Products   []map[string]any
	Page       int
	TotalPages int
}

// FilteredProductsPage fetches one page of the account product list
// (website.cpp:113).
func (c *Client) FilteredProductsPage(ctx context.Context, q ProductQuery, page int) (ProductPage, error) {
	return c.productPage(ctx, c.ep.www+"/account/getFilteredProducts"+q.queryString(page))
}

// WishlistPage fetches one page of the wishlist search (website.cpp:706).
func (c *Client) WishlistPage(ctx context.Context, page int) (ProductPage, error) {
	return c.productPage(ctx, c.ep.www+"/account/wishlist/search?hasHiddenProducts=false"+
		"&hiddenFlag=0&isUpdated=0&mediaType=0&sortBy=title&system=&page="+strconv.Itoa(page))
}

// queryString renders the query the way the C++ source concatenates it. The
// tags parameter is already a comma-separated list and is never re-encoded
// (review lock, D5), which is why url.Values is not used here.
func (q ProductQuery) queryString(page int) string {
	s := "?hiddenFlag=" + strconv.Itoa(q.HiddenFlag) +
		"&isUpdated=" + strconv.Itoa(q.IsUpdated) +
		"&mediaType=1&sortBy=title&system=&page=" + strconv.Itoa(page)
	if len(q.Tags) > 0 {
		s += "&tags=" + strings.Join(q.Tags, ",")
	}
	return s
}

// productPage decodes one listing page.
//
// Shape rules (review lock, D3):
//   - page and totalPages must be present integers; a missing or malformed
//     value is an error, so a schema change cannot masquerade as "no results"
//     (jsoncpp reads null as 0 and would end the walk silently);
//   - totalPages == 0 is a legitimate empty listing;
//   - a missing or non-array products field yields no products without an
//     error, matching `if (root["products"].isArray())` (website.cpp:134);
//   - a product that is not an object is an error.
func (c *Client) productPage(ctx context.Context, url string) (ProductPage, error) {
	root, err := c.getResponseJSON(ctx, url)
	if err != nil {
		return ProductPage{}, err
	}
	page, err := requiredInt(root, "page", url)
	if err != nil {
		return ProductPage{}, err
	}
	totalPages, err := requiredInt(root, "totalPages", url)
	if err != nil {
		return ProductPage{}, err
	}

	products := []map[string]any{}
	if arr, ok := root["products"].([]any); ok {
		for i, el := range arr {
			obj, err := jsonObject(el)
			if err != nil {
				return ProductPage{}, fmt.Errorf("webapi: %s: products[%d]: %w", url, i, err)
			}
			products = append(products, obj)
		}
	}
	return ProductPage{Products: products, Page: page, TotalPages: totalPages}, nil
}

// requiredInt reads an integer field that must be present.
func requiredInt(root map[string]any, key, url string) (int, error) {
	v, ok := root[key]
	if !ok || v == nil {
		return 0, fmt.Errorf("webapi: %s: missing %q", url, key)
	}
	n, err := jsonIntStrict(v)
	if err != nil {
		return 0, fmt.Errorf("webapi: %s: %q: %w", url, key, err)
	}
	return int(n), nil
}
