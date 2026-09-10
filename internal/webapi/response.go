package webapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/nekrozis/goggo/internal/jsonval"
)

// ErrNotJSON reports that a response body did not form the expected JSON
// document. It covers the SHAPE of a response only: HTTP failures surface as
// *httpx.StatusError and field-level problems are wrapped with context, so a
// caller can map ErrNotJSON onto its own hint — the C++ source prints
// "Response was not JSON. Cookies have most likely expired. Try --login
// first." (website.cpp:820).
//
// It stays here rather than in internal/jsonval because it describes an HTTP
// response shape, not a JSON value conversion.
var ErrNotJSON = errors.New("webapi: response was not JSON")

// getResponse fetches url and returns the body, mirroring
// Website::getResponse (website.cpp:28-58). The C++ implementation printed
// transport/HTTP errors to stdout and returned an empty string; here errors
// are returned instead so the caller decides how to report them (the S06
// ReadJSONFile convention applied to HTTP).
func (c *Client) getResponse(ctx context.Context, url string) (string, error) {
	body, err := c.hx.GetBytesWithRetry(ctx, url)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// getResponseJSON fetches url and decodes the body as a JSON object,
// mirroring Website::getResponseJson (website.cpp:60-81). C++ returned an
// empty Json::Value on parse failure after printing; Go returns an error.
func (c *Client) getResponseJSON(ctx context.Context, url string) (map[string]any, error) {
	body, err := c.getResponse(ctx, url)
	if err != nil {
		return nil, err
	}
	return decodeJSONObject(body)
}

// decodeJSONObject decodes a JSON object body. A body that is empty, malformed
// or not a JSON object is reported as ErrNotJSON: this is a response-SHAPE
// problem, which callers may want to translate into their own hint (the C++
// source prints the "--login" advice when the account response is not JSON,
// website.cpp:820). HTTP-level failures never reach here — getResponse has
// already turned >= 400 into a *httpx.StatusError.
func decodeJSONObject(body string) (map[string]any, error) {
	if strings.TrimSpace(body) == "" {
		return nil, fmt.Errorf("%w: empty body", ErrNotJSON)
	}
	var v any
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNotJSON, err)
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: got %s", ErrNotJSON, jsonval.Kind(v))
	}
	return obj, nil
}
