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

	"github.com/nekrozis/goggo/internal/config"
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

// Client drives the Galaxy content API over an httpx transport.
type Client struct {
	ep     endpoints
	galaxy *config.GalaxyConfig
	hx     *httpx.Client
}

// New builds a Client on a caller-provided transport and token store.
//
// The transport is owned by the caller: galaxy never creates one, so retry
// policy, timeouts, the low-speed guard and the cookie jar are configured in
// exactly one place, and the same handle can serve the website API and the
// content API.
//
// Both arguments are required; a nil one is an error.
func New(hx *httpx.Client, galaxy *config.GalaxyConfig) (*Client, error) {
	if galaxy == nil {
		return nil, errors.New("galaxy: nil galaxy config")
	}
	if hx == nil {
		return nil, errors.New("galaxy: nil http client")
	}
	return &Client{
		ep:     defaultEndpoints(),
		galaxy: galaxy,
		hx:     hx,
	}, nil
}

// ErrNotJSON reports that a response body did not form a JSON object. It covers
// the SHAPE of a response only: HTTP failures surface as *httpx.StatusError and
// field-level problems are wrapped with context by the caller.
//
// It is this package's own sentinel rather than a shared one, because the
// website API in internal/webapi draws the same distinction for its own
// responses.
var ErrNotJSON = errors.New("galaxy: response was not JSON")

// bearer returns the token to attach, or "" when no Authorization header must
// be sent. An expired store never contributes a token, and neither does an
// empty one: the request then goes out unauthenticated and the server's answer
// decides the outcome.
func (c *Client) bearer() string {
	if c.galaxy.IsExpired() {
		return ""
	}
	return c.galaxy.GetAccessToken()
}

// getResponse fetches target and returns the body.
//
// Acceptance of encodings is left to the transport, which asks for gzip and
// decompresses it transparently. Setting Accept-Encoding here would DISABLE
// Go's transparent decompression, so it is deliberately not touched.
func (c *Client) getResponse(ctx context.Context, target string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", err
	}
	if tok := c.bearer(); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
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
// non-object body is ErrNotJSON. A JSON array is therefore a failure, not a
// success with the wrong type, so the steps that consume these documents
// work against a known object boundary.
//
// The zlib retry is attempted strictly AFTER the first decode failed, and only
// when the first two bytes are a zlib stream header. Ordinary malformed JSON is
// never inflated, so its error stays a single ErrNotJSON. Inflation failures are
// not reported separately — the body was not a usable JSON object either way,
// and the first error describes what the caller asked for.
//
// The object assertion is what makes this the object-shaped entry point;
// decodeDocument below is the same pipeline without it.
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
// zlib retry decodeJSONObject used to own.
//
// It exists because the Galaxy product documents carry a top-level ARRAY in two
// places: the response of dlcs.expanded_all_products_url and that of
// products?ids=…. Callers that need an object assert one on the result
// (decodeJSONObject does exactly that); the assertion lives there rather than
// here, so the value-typed half stays reachable.
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
// The header check is what limits the fallback to the compressed case: 0x78
// followed by 0x01, 0x5e, 0x9c or 0xda is a zlib stream with a 32 KiB window
// and the usual compression levels. It is written as two byte tests rather than
// through a shared uint16 reader, because nothing else needs one.
//
// compress/zlib handles the zlib wrapper itself, so no header is parsed out.
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
