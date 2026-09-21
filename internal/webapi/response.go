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

// decodeJSONObject decodes a JSON object body into a Go value. A body that is
// empty, malformed or not a JSON object is reported as ErrNotJSON: this is a
// response-SHAPE problem, which callers may want to translate into their own hint
// (the CLI renders the "--login" advice). HTTP-level failures never reach here —
// getResponse has already turned >= 400 into a *httpx.StatusError.
func decodeJSONObject(body string) (map[string]any, error) {
	var obj map[string]any
	if err := decodeObject([]byte(body), &obj); err != nil {
		return nil, err
	}
	return obj, nil
}

// requireJSONObject reports whether body is exactly one complete JSON object.
//
// It is a pure shape gate: it does not trim, does not build a Go value and does
// not reorder members, and it neither clones nor modifies the caller's body.
// That last part is the point — the bytes that pass here are handed on as the
// document, so a gate that rewrote or normalised them would hand on something
// the server did not send. The decoder does read the body into its own buffer;
// the guarantee is about the caller's slice, not about the decoder.
func requireJSONObject(body []byte) error {
	if len(body) == 0 {
		return fmt.Errorf("%w: empty body", ErrNotJSON)
	}
	dec := jsontext.NewDecoder(bytes.NewReader(body))
	val, err := dec.ReadValue()
	if err != nil {
		return fmt.Errorf("%w: %w", ErrNotJSON, err)
	}
	// The kind has to be taken before the decoder is used again: ReadValue's
	// result aliases the decoder's internal buffer, and the next read reuses it.
	kind := val.Kind()
	switch _, err := dec.ReadToken(); {
	case err == nil:
		return fmt.Errorf("%w: trailing content after the top-level value", ErrNotJSON)
	case errors.Is(err, io.EOF):
		// exactly one complete top-level value, as the shape contract requires
	default:
		return fmt.Errorf("%w: %w", ErrNotJSON, err)
	}
	if kind != jsontext.KindBeginObject {
		return fmt.Errorf("%w: got %s", ErrNotJSON, kind)
	}
	return nil
}

// decodeObject decodes an object-shaped JSON body into target. An empty,
// malformed or non-object payload is ErrNotJSON, and the shape decision itself
// belongs to requireJSONObject.
//
// The decoder is given the ORIGINAL bytes: whitespace the format does not accept
// has to reach it and be rejected, which trimming the body first would prevent.
func decodeObject(body []byte, target any) error {
	if err := requireJSONObject(body); err != nil {
		return err
	}
	if err := jsonv2.Unmarshal(body, target); err != nil {
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
