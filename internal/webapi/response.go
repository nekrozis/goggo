package webapi

import (
	"context"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"strings"
)

// ErrNotJSON reports that a response body did not form the expected JSON
// document. It covers the SHAPE of a response only: HTTP failures surface as
// *httpx.StatusError and field-level problems are wrapped with context, so a
// caller can map ErrNotJSON onto its own hint (the CLI renders the "--login"
// advice).
var ErrNotJSON = errors.New("webapi: response was not JSON")

// getResponse fetches url and returns the body. Transport and HTTP errors are
// returned rather than printed, so the caller decides how to report them.
func (c *Client) getResponse(ctx context.Context, url string) (string, error) {
	body, err := c.hx.GetBytesWithRetry(ctx, url)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// getResponseJSON fetches url and decodes the body as a JSON object. A body that
// is not an object is an error.
func (c *Client) getResponseJSON(ctx context.Context, url string) (map[string]jsontext.Value, error) {
	body, err := c.getResponse(ctx, url)
	if err != nil {
		return nil, err
	}
	return decodeJSONObject(body)
}

// decodeJSONObject decodes a JSON object body. A body that is empty, malformed
// or not a JSON object is reported as ErrNotJSON: this is a response-SHAPE
// problem, which callers may want to translate into their own hint (the CLI
// renders the "--login" advice). HTTP-level failures never reach here —
// getResponse has already turned >= 400 into a *httpx.StatusError.
//
// Members are kept as raw JSON text rather than decoded Go values: a number
// then keeps the literal the server sent, and nothing is paid for the parts of
// a document this program never reads.
func decodeJSONObject(body string) (map[string]jsontext.Value, error) {
	if strings.TrimSpace(body) == "" {
		return nil, fmt.Errorf("%w: empty body", ErrNotJSON)
	}
	var v jsontext.Value
	if err := jsonv2.Unmarshal([]byte(body), &v); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNotJSON, err)
	}
	obj, err := memberObject(v)
	if err != nil {
		return nil, fmt.Errorf("%w: got %s", ErrNotJSON, jsonKind(v))
	}
	if obj == nil {
		// memberObject reports absent/null as "not present"; a body of "null"
		// is not an object either.
		return nil, fmt.Errorf("%w: got %s", ErrNotJSON, jsonKind(v))
	}
	return obj, nil
}
