package galaxy

import (
	"compress/zlib"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/nekrozis/goggo/internal/httpx"
	"github.com/nekrozis/goggo/internal/jsonval"
)

// DefaultContentSystemHost serves builds, secure links and dependency
// repository documents.
const DefaultContentSystemHost = "https://content-system.gog.com"

// DefaultCDNHost serves the manifests themselves.
const DefaultCDNHost = "https://cdn.gog.com"

// DefaultAPIHost serves the product documents. It is a different host from the
// content system, and the two are not interchangeable.
const DefaultAPIHost = "https://api.gog.com"

// endpoints groups the per-host URL prefixes of one Client.
type endpoints struct {
	contentSystem string
	cdn           string
	api           string
}

func defaultEndpoints() endpoints {
	return endpoints{contentSystem: DefaultContentSystemHost, cdn: DefaultCDNHost, api: DefaultAPIHost}
}

// AuthorizationSource is what the Galaxy API client needs from the credential
// seam: the header value, never the token behind it.
type AuthorizationSource interface{ AuthorizationValue() string }

// Client drives the Galaxy content API over an httpx transport.
type Client struct {
	ep    endpoints
	authz AuthorizationSource
	hx    *httpx.Client
}

// New builds a Client on a caller-provided transport and credential source.
// Both arguments are required; a nil one is an error.
func New(hx *httpx.Client, authz AuthorizationSource) (*Client, error) {
	if authz == nil {
		return nil, errors.New("galaxy: nil galaxy config")
	}
	if hx == nil {
		return nil, errors.New("galaxy: nil http client")
	}
	return &Client{
		ep:    defaultEndpoints(),
		authz: authz,
		hx:    hx,
	}, nil
}

// ErrNotJSON reports that a response body did not form a JSON object. It covers
// the SHAPE of a response only: HTTP failures surface as *httpx.StatusError, and
// field-level problems are wrapped with context by the caller.
var ErrNotJSON = errors.New("galaxy: response was not JSON")

// authorization returns the Authorization header value, or "" when no header
// must be sent. An expired or empty store never contributes a value: the request
// then goes out unauthenticated and the server's answer decides the outcome.
func (c *Client) authorization() string {
	return c.authz.AuthorizationValue()
}

// getResponse fetches target and returns the body.
//
// Acceptance of encodings is left to the transport, which asks for gzip and
// decompresses it transparently. Setting Accept-Encoding here would DISABLE
// Go's transparent decompression, so it is deliberately not touched.
func (c *Client) getResponse(ctx context.Context, target string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", httpx.SanitizeError(err)
	}
	if v := c.authorization(); v != "" {
		req.Header.Set("Authorization", v)
	}
	body, err := c.hx.DoBytesWithRetry(ctx, req)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// ResponseJSON fetches a Galaxy API document as JSON over an authenticated
// GET. The website path resolves its downlink documents through it.
func (c *Client) ResponseJSON(ctx context.Context, target string) (map[string]any, error) {
	return c.getResponseJSON(ctx, target)
}

// Response fetches a Galaxy API document as text — the checksum XML behind a
// website file's downlink.
func (c *Client) Response(ctx context.Context, target string) (string, error) {
	return c.getResponse(ctx, target)
}

// getResponseJSON fetches target and decodes the body as a JSON object,
// including the zlib retry described on decodeJSONObject.
func (c *Client) getResponseJSON(ctx context.Context, target string) (map[string]any, error) {
	body, err := c.getResponse(ctx, target)
	if err != nil {
		return nil, err
	}
	return decodeJSONObject(body)
}

// decodeJSONObject decodes a JSON object, retrying once through zlib when the
// body looks like a compressed stream.
//
// The shape contract is single-entry and explicit: an empty, malformed or
// non-object body is ErrNotJSON, so a JSON array is a failure rather than a
// success with the wrong type. The zlib retry is attempted strictly AFTER the
// first decode failed and only when the first two bytes are a zlib stream
// header, so ordinary malformed JSON is never inflated and its error stays a
// single ErrNotJSON; inflation failures are not reported separately, since the
// first error already describes what the caller asked for.
func decodeJSONObject(body string) (map[string]any, error) {
	v, err := decodeDocument(body)
	if err != nil {
		return nil, err
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: got %s", ErrNotJSON, jsonval.Kind(v))
	}
	return obj, nil
}

// decodeDocument decodes any JSON document — object, array or scalar — with the
// zlib retry. It exists because two Galaxy product documents are top-level
// arrays: the responses of dlcs.expanded_all_products_url and products?ids=….
// Callers that need an object assert one on the result (decodeJSONObject does
// that), so the assertion lives there and not here.
func decodeDocument(body string) (any, error) {
	v, err := decodeAny(body)
	if err == nil {
		return v, nil
	}
	if plain, ok := inflateZlibBody(body); ok {
		if v, retryErr := decodeAny(string(plain)); retryErr == nil {
			return v, nil
		}
	}
	return nil, err
}

// decodeAny decodes one JSON document of any type, with no compression
// handling and no shape opinion.
func decodeAny(body string) (any, error) {
	if strings.TrimSpace(body) == "" {
		return nil, fmt.Errorf("%w: empty body", ErrNotJSON)
	}
	var v any
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNotJSON, err)
	}
	return v, nil
}

// inflateZlibBody inflates body when it starts with a zlib stream header, and
// reports whether it did.
//
// The header check limits the fallback to the compressed case: 0x78 followed by
// 0x01, 0x5e, 0x9c or 0xda is a zlib stream with a 32 KiB window and the usual
// compression levels. compress/zlib handles the zlib wrapper itself, so no header
// is parsed out here.
func inflateZlibBody(body string) ([]byte, bool) {
	if len(body) < 2 || body[0] != 0x78 {
		return nil, false
	}
	switch body[1] {
	case 0x01, 0x5e, 0x9c, 0xda:
	default:
		return nil, false
	}
	zr, err := zlib.NewReader(strings.NewReader(body))
	if err != nil {
		return nil, false
	}
	defer zr.Close()
	plain, err := io.ReadAll(zr)
	if err != nil {
		return nil, false
	}
	return plain, true
}
