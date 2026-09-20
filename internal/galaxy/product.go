package galaxy

import (
	"context"
	"fmt"
	"strings"

	"github.com/nekrozis/goggo/internal/jsonval"
)

// Both product endpoints carry the same expand list.
const (
	productExpand = "downloads,description,screenshots,videos,related_products,changelog"
	// maxDLCBatchSize is max_ids: the largest ids= list one request carries.
	maxDLCBatchSize = 45
	// expandedDLCsKey is the member the expansion result is injected under. The
	// conversion layer reads it from there (internal/gamedetails).
	expandedDLCsKey = "expanded_dlcs"
)

// Product fetches a product document and expands its DLCs into it.
//
// The result is the RAW document the API answered, plus the expanded_dlcs
// member, and deliberately not a domain object: assembling GameDetails belongs
// to internal/gamedetails, and keeping acquisition and conversion apart is what
// lets this function be tested against request shapes alone. A document without
// DLCs gets no such member.
//
// A member this function reads is validated against the JSON type it must have:
// an absent member is the zero value and a present member of the wrong type is
// an error. Nothing is coerced into something plausible.
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
// A dlcs member that is absent, null or not an object is skipped — no request,
// no expanded_dlcs, the document returned as the API answered it. The live API
// really does send the non-object shapes: a majority of one probed account's
// products carry "dlcs": [] (D52, evidence in
// dev/audit/evidence/D52-dlcs-census.txt). Inside an object the fields stay
// strict (D52), because a lenient read has no observed sample to justify
// widening them.
func (c *Client) expandDLCs(ctx context.Context, product map[string]any) error {
	raw, present := product["dlcs"]
	dlcs, isObject := raw.(map[string]any)
	if !present || raw == nil || !isObject {
		return nil
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
// which takes the single-request branch; a present member that is not an array
// is an error.
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

// expandDLCsInOneRequest is the branch for at most maxDLCBatchSize DLCs.
func (c *Client) expandDLCsInOneRequest(ctx context.Context, product, dlcs map[string]any) error {
	url, err := dlcExpandedURL(dlcs)
	if err != nil {
		return err
	}
	if url == "" {
		// An empty url means "no request, empty result".
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

// expandDLCsInBatches is the branch above maxDLCBatchSize.
//
// A batch goes out when it is full OR when the list ends. The second condition
// keeps a count that is an exact multiple of maxDLCBatchSize from sending a
// trailing empty request.
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

// dlcID reads one dlcs.products entry's id. It is the identifier family, not a
// string test: an absent or null member is the empty string, and a number —
// which is what the live API actually sends here — is stringified. Only a
// structured value is an error.
func dlcID(entry any, index int) (string, error) {
	obj, err := jsonval.Object(entry)
	if err != nil {
		return "", fmt.Errorf("dlcs.products[%d]: %w", index, err)
	}
	raw, present := obj["id"]
	if !present || raw == nil {
		return "", nil
	}
	id, err := jsonval.Str(raw)
	if err != nil {
		return "", fmt.Errorf("dlcs.products[%d].id: %w", index, err)
	}
	return id, nil
}

// fetchDLCBatch fetches one expansion response and requires it to be an array,
// which is the shape both call sites read.
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
