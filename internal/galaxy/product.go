package galaxy

import (
	"context"
	"fmt"
	"strings"

	"github.com/nekrozis/goggo/internal/jsonval"
)

// The product endpoints of galaxyapi.cpp:354-401. Both carry the same expand
// list, spelled the way the C++ source spells it.
const (
	productExpand = "downloads,description,screenshots,videos,related_products,changelog"
	// maxDLCBatchSize is max_ids: the largest ids= list one request carries.
	maxDLCBatchSize = 45
	// expandedDLCsKey is the member the expansion result is injected under. The
	// conversion layer reads it from there (internal/gamedetails).
	expandedDLCsKey = "expanded_dlcs"
)

// Product fetches a product document and expands its DLCs into it
// (galaxyapi.cpp:354-401).
//
// The result is the RAW document — the one the API answered, plus the
// expanded_dlcs member — and deliberately not a domain object. Assembling
// GameDetails belongs to the conversion layer (GD1), and keeping acquisition
// and conversion apart is what lets this function be tested against request
// shapes alone.
//
// Field reading follows the package's usual split: a member this function
// reads is validated against the JSON type it must have, so an absent member is
// the zero value and a present member of the wrong type is an error. Nothing is
// coerced into something plausible.
//
// The DLC expansion has two branches (galaxyapi.cpp:360-399):
//
//	dlcs.products holds at most 45 ids
//	    → one request to dlcs.expanded_all_products_url, whose response is the
//	      array of DLC documents
//	dlcs.products holds more
//	    → ids are accumulated and sent in batches of maxDLCBatchSize through
//	      products?ids=…, each response array appended in order
//
// Either way the collected array is stored under expanded_dlcs. A document
// without DLCs gets no such member, exactly as upstream leaves it.
func (c *Client) Product(ctx context.Context, productID string) (map[string]any, error) {
	body, err := c.getResponse(ctx, c.ep.api+"/products/"+productID+"?expand="+productExpand)
	if err != nil {
		return nil, err
	}
	// The main document is read through the object-shaped entry point, so its
	// shape contract is the one every other endpoint in this package has.
	product, err := decodeJSONObject(body)
	if err != nil {
		return nil, err
	}
	if err := c.expandDLCs(ctx, product); err != nil {
		return nil, err
	}
	return product, nil
}

// expandDLCs fetches the DLC documents of a product document and injects them.
//
// The dlcs member decides whether anything happens at all:
//
//	absent      → nothing to expand, nothing injected
//	null        → same: a null is how an optional member is commonly spelled, so
//	              it carries no shape information to complain about
//	object      → expand
//	anything else → error: the member is there and says something other than
//	              "here are the DLCs"
func (c *Client) expandDLCs(ctx context.Context, product map[string]any) error {
	raw, present := product["dlcs"]
	if !present || raw == nil {
		return nil
	}
	dlcs, err := jsonval.Object(raw)
	if err != nil {
		return fmt.Errorf("dlcs: %w", err)
	}
	products, err := dlcProducts(dlcs)
	if err != nil {
		return err
	}
	if len(products) <= maxDLCBatchSize {
		return c.expandDLCsInOneRequest(ctx, product, dlcs)
	}
	return c.expandDLCsInBatches(ctx, product, products)
}

// dlcProducts reads dlcs.products. An absent or null member is the empty list,
// which upstream sizes as 0 and therefore sends down the single-request branch
// (galaxyapi.cpp:359); a present member that is not an array is an error.
func dlcProducts(dlcs map[string]any) ([]any, error) {
	raw, present := dlcs["products"]
	if !present || raw == nil {
		return nil, nil
	}
	items, err := jsonval.Array(raw)
	if err != nil {
		return nil, fmt.Errorf("dlcs.products: %w", err)
	}
	return items, nil
}

// expandDLCsInOneRequest is the branch for at most maxDLCBatchSize DLCs
// (galaxyapi.cpp:367-370).
func (c *Client) expandDLCsInOneRequest(ctx context.Context, product, dlcs map[string]any) error {
	url, err := dlcExpandedURL(dlcs)
	if err != nil {
		return err
	}
	if url == "" {
		// Upstream requests the empty url here, which is a case C++ leaves
		// undefined; this port defines it as "no request, empty result".
		product[expandedDLCsKey] = []any{}
		return nil
	}
	docs, err := c.fetchDLCBatch(ctx, url)
	if err != nil {
		return err
	}
	product[expandedDLCsKey] = docs
	return nil
}

// dlcExpandedURL reads dlcs.expanded_all_products_url: absent or null is the
// empty string, a present non-string is an error.
func dlcExpandedURL(dlcs map[string]any) (string, error) {
	raw, present := dlcs["expanded_all_products_url"]
	if !present || raw == nil {
		return "", nil
	}
	url, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("dlcs.expanded_all_products_url: expected a JSON string, got %s", jsonval.Kind(raw))
	}
	return url, nil
}

// expandDLCsInBatches is the branch above maxDLCBatchSize (galaxyapi.cpp:371-399).
//
// A batch goes out when it is full OR when the list ends, which is the pair of
// conditions in the original. The second one is what keeps a count that is an
// exact multiple of maxDLCBatchSize from sending a trailing empty request.
func (c *Client) expandDLCsInBatches(ctx context.Context, product map[string]any, products []any) error {
	expanded := make([]any, 0, len(products))
	ids := make([]string, 0, maxDLCBatchSize)
	for i, entry := range products {
		id, err := dlcID(entry, i)
		if err != nil {
			return err
		}
		ids = append(ids, id)
		if len(ids) == maxDLCBatchSize || i == len(products)-1 {
			target := c.ep.api + "/products?ids=" + strings.Join(ids, ",") + "&expand=" + productExpand
			docs, err := c.fetchDLCBatch(ctx, target)
			if err != nil {
				return err
			}
			expanded = append(expanded, docs...)
			ids = ids[:0]
		}
	}
	product[expandedDLCsKey] = expanded
	return nil
}

// dlcID reads one dlcs.products entry's id: an absent or null member is the
// empty string, which is what upstream's asString() makes of it.
func dlcID(entry any, index int) (string, error) {
	obj, err := jsonval.Object(entry)
	if err != nil {
		return "", fmt.Errorf("dlcs.products[%d]: %w", index, err)
	}
	raw, present := obj["id"]
	if !present || raw == nil {
		return "", nil
	}
	id, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("dlcs.products[%d].id: expected a JSON string, got %s", index, jsonval.Kind(raw))
	}
	return id, nil
}

// fetchDLCBatch fetches one expansion response and requires it to be an array,
// which is the shape both call sites read (galaxyapi.cpp:369,395).
//
// The failure names the member rather than the url: the url is part of the
// document, and this package keeps urls out of error strings.
func (c *Client) fetchDLCBatch(ctx context.Context, url string) ([]any, error) {
	body, err := c.getResponse(ctx, url)
	if err != nil {
		return nil, err
	}
	doc, err := decodeDocument(body)
	if err != nil {
		return nil, err
	}
	docs, err := jsonval.Array(doc)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", expandedDLCsKey, err)
	}
	return docs, nil
}
