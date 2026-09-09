package webapi

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

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

// decodeJSONObject decodes a JSON object body. An empty body is an error
// (there is no "null object" convention to preserve from the C++ side here).
func decodeJSONObject(body string) (map[string]any, error) {
	if strings.TrimSpace(body) == "" {
		return nil, fmt.Errorf("webapi: empty JSON response")
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(body), &obj); err != nil {
		return nil, err
	}
	return obj, nil
}
