package webapi

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"io"
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

func (c *Client) getResponseBytes(ctx context.Context, url string) ([]byte, error) {
	return c.hx.GetBytesWithRetry(ctx, url)
}

// getResponseJSON fetches url and decodes the body as a JSON object. A body that
// is not an object is an error.
func (c *Client) getResponseJSON(ctx context.Context, url string) (map[string]any, error) {
	body, err := c.getResponseBytes(ctx, url)
	if err != nil {
		return nil, err
	}
	var obj map[string]any
	if err := decodeObject(body, &obj); err != nil {
		return nil, err
	}
	return obj, nil
}

// decodeJSONObject decodes a JSON object body. A body that is empty, malformed
// or not a JSON object is reported as ErrNotJSON: this is a response-SHAPE
// problem, which callers may want to translate into their own hint (the CLI
// renders the "--login" advice). HTTP-level failures never reach here —
// getResponse has already turned >= 400 into a *httpx.StatusError.
func decodeJSONObject(body string) (map[string]any, error) {
	var obj map[string]any
	if err := decodeObject([]byte(body), &obj); err != nil {
		return nil, err
	}
	return obj, nil
}

// decodeObject decodes an object-shaped JSON body. Empty, whitespace, malformed,
// or non-object payloads are returned as ErrNotJSON.
func decodeObject(body []byte, target any) error {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return fmt.Errorf("%w: empty body", ErrNotJSON)
	}
	dec := jsontext.NewDecoder(bytes.NewReader(trimmed))
	tok, err := dec.ReadToken()
	if err != nil {
		return fmt.Errorf("%w: %w", ErrNotJSON, err)
	}
	if tok.Kind() != jsontext.KindBeginObject {
		return fmt.Errorf("%w: got %s", ErrNotJSON, tok.Kind())
	}
	if err := jsonv2.Unmarshal(trimmed, target); err != nil {
		return fmt.Errorf("%w: %w", ErrNotJSON, err)
	}
	return nil
}

func token(v jsontext.Value) (jsontext.Token, error) {
	if len(v) == 0 {
		return jsontext.Token{}, io.ErrUnexpectedEOF
	}
	dec := jsontext.NewDecoder(bytes.NewReader(v))
	return dec.ReadToken()
}
