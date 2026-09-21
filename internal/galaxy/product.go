package galaxy

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"fmt"
	"strings"
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

// ProductDocument carries the raw payload of a Galaxy product and the
// metadata needed by consumers.
type ProductDocument struct {
	// JSON owns the encoded product document bytes.
	// When no DLC expansion is performed it is the API response bytes;
	// when DLCs are expanded it is the augmented document encoding.
	JSON []byte

	// Slug is the product's slug (gamename), if present.
	Slug string

	// Title is the product's human-readable title, if present.
	Title string
}

type rawDLCs struct {
	Products               jsontext.Value `json:"products"`
	ExpandedAllProductsURL jsontext.Value `json:"expanded_all_products_url"`
}

type rawDLCEntry struct {
	ID jsontext.Value `json:"id"`
}

// Product fetches a product document and expands its DLCs into it.
func (c *Client) Product(ctx context.Context, productID string) (ProductDocument, error) {
	raw, err := c.getResponseBytes(ctx, c.ep.api+"/products/"+productID+"?expand="+productExpand)
	if err != nil {
		return ProductDocument{}, err
	}
	if plain, ok := inflateZlibBytes(raw); ok {
		raw = plain
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return ProductDocument{}, fmt.Errorf("%w: empty body", ErrNotJSON)
	}

	var doc map[string]jsontext.Value
	if err := jsonv2.Unmarshal(raw, &doc); err != nil {
		return ProductDocument{}, fmt.Errorf("%w: %w", ErrNotJSON, err)
	}

	prodDoc := ProductDocument{
		JSON:  bytes.Clone(raw),
		Slug:  readStringToken(doc["slug"]),
		Title: readStringToken(doc["title"]),
	}

	rawDLCs, present := doc["dlcs"]
	if !present || len(rawDLCs) == 0 || rawDLCs.Kind() != jsontext.KindBeginObject {
		// D52: absent, null or not an object is skipped — no request,
		// no expanded_dlcs, document returned as the API answered it.
		return prodDoc, nil
	}

	expanded, err := c.expandDLCs(ctx, rawDLCs)
	if err != nil {
		return ProductDocument{}, err
	}

	expandedBytes, err := jsonv2.Marshal(expanded)
	if err != nil {
		return ProductDocument{}, err
	}
	doc[expandedDLCsKey] = jsontext.Value(expandedBytes)
	finalJSON, err := jsonv2.Marshal(doc)
	if err != nil {
		return ProductDocument{}, err
	}
	prodDoc.JSON = finalJSON
	return prodDoc, nil
}

func readStringToken(v jsontext.Value) string {
	if len(v) == 0 {
		return ""
	}
	dec := jsontext.NewDecoder(bytes.NewReader(v))
	tok, err := dec.ReadToken()
	if err != nil || tok.Kind() != jsontext.KindString {
		return ""
	}
	return tok.String()
}

// expandDLCs fetches the DLC documents of a product document and injects them.
func (c *Client) expandDLCs(ctx context.Context, rawDLCsVal jsontext.Value) ([]jsontext.Value, error) {
	var info rawDLCs
	if err := jsonv2.Unmarshal(rawDLCsVal, &info); err != nil {
		return nil, fmt.Errorf("dlcs: %w", err)
	}

	products, err := dlcProducts(info.Products)
	if err != nil {
		return nil, err
	}

	if len(products) <= maxDLCBatchSize {
		return c.expandDLCsInOneRequest(ctx, info.ExpandedAllProductsURL)
	}
	return c.expandDLCsInBatches(ctx, products)
}

func dlcProducts(raw jsontext.Value) ([]rawDLCEntry, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	if raw.Kind() == jsontext.KindNull {
		return nil, nil
	}
	if raw.Kind() != jsontext.KindBeginArray {
		return nil, fmt.Errorf("dlcs.products: expected array, got %s", raw.Kind())
	}
	var items []rawDLCEntry
	if err := jsonv2.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("dlcs.products: %w", err)
	}
	return items, nil
}

func (c *Client) expandDLCsInOneRequest(ctx context.Context, rawURL jsontext.Value) ([]jsontext.Value, error) {
	url, err := dlcExpandedURL(rawURL)
	if err != nil {
		return nil, err
	}
	if url == "" {
		return []jsontext.Value{}, nil
	}
	return c.fetchDLCBatch(ctx, url)
}

func dlcExpandedURL(raw jsontext.Value) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	dec := jsontext.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.ReadToken()
	if err != nil {
		return "", err
	}
	switch tok.Kind() {
	case jsontext.KindNull:
		return "", nil
	case jsontext.KindString:
		return tok.String(), nil
	default:
		return "", fmt.Errorf("dlcs.expanded_all_products_url: expected a JSON string, got %s", tok.Kind())
	}
}

func (c *Client) expandDLCsInBatches(ctx context.Context, products []rawDLCEntry) ([]jsontext.Value, error) {
	expanded := make([]jsontext.Value, 0, len(products))
	ids := make([]string, 0, maxDLCBatchSize)
	for i, entry := range products {
		id, err := dlcID(entry.ID, i)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
		if len(ids) == maxDLCBatchSize || i == len(products)-1 {
			target := c.ep.api + "/products?ids=" + strings.Join(ids, ",") + "&expand=" + productExpand
			docs, err := c.fetchDLCBatch(ctx, target)
			if err != nil {
				return nil, err
			}
			expanded = append(expanded, docs...)
			ids = ids[:0]
		}
	}
	return expanded, nil
}

func dlcID(raw jsontext.Value, index int) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	dec := jsontext.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.ReadToken()
	if err != nil {
		return "", fmt.Errorf("dlcs.products[%d].id: %w", index, err)
	}
	switch tok.Kind() {
	case jsontext.KindNull:
		return "", nil
	case jsontext.KindString, jsontext.KindNumber:
		return tok.String(), nil
	default:
		return "", fmt.Errorf("dlcs.products[%d].id: expected scalar, got %s", index, tok.Kind())
	}
}

func (c *Client) fetchDLCBatch(ctx context.Context, url string) ([]jsontext.Value, error) {
	raw, err := c.getResponseBytes(ctx, url)
	if err != nil {
		return nil, err
	}
	if plain, ok := inflateZlibBytes(raw); ok {
		raw = plain
	}
	var batch []jsontext.Value
	if err := jsonv2.Unmarshal(raw, &batch); err != nil {
		return nil, fmt.Errorf("%s: %w", expandedDLCsKey, err)
	}
	return batch, nil
}
