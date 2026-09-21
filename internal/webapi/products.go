package webapi

import (
	"context"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"fmt"
	"strconv"
	"strings"
)

// ProductQuery is one getFilteredProducts request.
type ProductQuery struct {
	Tags       []string
	HiddenFlag int
	IsUpdated  int
}

// ProductPage is one decoded page of a product or wishlist listing. Products
// holds the raw product objects so the assembly layer can map them
// without re-fetching.
type ProductPage struct {
	Products   []map[string]any
	Page       int
	TotalPages int
}

// FilteredProductsPage fetches one page of the account product list.
func (c *Client) FilteredProductsPage(ctx context.Context, q ProductQuery, page int) (ProductPage, error) {
	return c.productPage(ctx, c.ep.www+"/account/getFilteredProducts"+q.queryString(page))
}

// WishlistPage fetches one page of the wishlist search.
func (c *Client) WishlistPage(ctx context.Context, page int) (ProductPage, error) {
	return c.productPage(ctx, c.ep.www+"/account/wishlist/search?hasHiddenProducts=false"+
		"&hiddenFlag=0&isUpdated=0&mediaType=0&sortBy=title&system=&page="+strconv.Itoa(page))
}

// queryString renders the query by concatenation. The tags parameter is already
// a comma-separated list and is never re-encoded, which is why url.Values is
// not used here.
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
// Shape rules:
//   - page and totalPages must be present integers; a missing or malformed value
//     is an error, so a schema change cannot masquerade as "no results";
//   - totalPages == 0 is a legitimate empty listing;
//   - a missing or non-array products field yields no products without an error;
//   - a product that is not an object is an error.
func (c *Client) productPage(ctx context.Context, url string) (ProductPage, error) {
	body, err := c.getResponseBytes(ctx, url)
	if err != nil {
		return ProductPage{}, err
	}
	var root map[string]jsontext.Value
	if err := decodeObject(body, &root); err != nil {
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
	if rawProducts, ok := root["products"]; ok && len(rawProducts) > 0 && rawProducts.Kind() == jsontext.KindBeginArray {
		var items []jsontext.Value
		if err := jsonv2.Unmarshal(rawProducts, &items); err != nil {
			return ProductPage{}, fmt.Errorf("webapi: %s: products: %w", url, err)
		}
		products = make([]map[string]any, 0, len(items))
		for i, item := range items {
			if item.Kind() != jsontext.KindBeginObject {
				return ProductPage{}, fmt.Errorf("webapi: %s: products[%d]: expected a JSON object, got %s", url, i, item.Kind())
			}
			var obj map[string]any
			if err := jsonv2.Unmarshal(item, &obj); err != nil {
				return ProductPage{}, fmt.Errorf("webapi: %s: products[%d]: %w", url, i, err)
			}
			products = append(products, obj)
		}
	}
	return ProductPage{Products: products, Page: page, TotalPages: totalPages}, nil
}

// requiredInt reads an integer field that must be present.
func requiredInt(root map[string]jsontext.Value, key, url string) (int, error) {
	v, ok := root[key]
	if !ok || len(v) == 0 {
		return 0, fmt.Errorf("webapi: %s: missing %q", url, key)
	}
	if v.Kind() == jsontext.KindNull {
		return 0, fmt.Errorf("webapi: %s: %q: expected a JSON integer, got null", url, key)
	}
	if v.Kind() != jsontext.KindNumber {
		return 0, fmt.Errorf("webapi: %s: %q: expected a JSON integer, got %s", url, key, v.Kind())
	}
	// jsontext.Value retains the original JSON token bytes.
	s := string(v)
	if strings.Contains(s, ".") || strings.ContainsAny(s, "eE") {
		return 0, fmt.Errorf("webapi: %s: %q: expected a JSON integer, got non-integral %s", url, key, s)
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("webapi: %s: %q: %w", url, key, err)
	}
	return int(n), nil
}
