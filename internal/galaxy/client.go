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

// DefaultContentSystemHost is the content-system host that serves build,
// manifest and link data (galaxyapi.cpp:192,199,231,238). The CDN host used by
// the manifest and link resolution steps is added when those steps are ported
// (S14/S15), not before.
const DefaultContentSystemHost = "https://content-system.gog.com"

// endpoints groups the per-host URL prefixes of one Client.
type endpoints struct {
	contentSystem string
}

func defaultEndpoints() endpoints {
	return endpoints{contentSystem: DefaultContentSystemHost}
}

// Client drives the Galaxy content API over an httpx transport.
//
// Fields are ordered to minimise padding: the endpoint block (one string, 16B)
// first, then the pointers (8B each).
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
// It is this package's own sentinel rather than a shared one, mirroring the C++
// source, where galaxyAPI and Website each carry their own getResponseJson
// (galaxyapi.cpp:135-188 and website.cpp:60-81 are separate implementations).
var ErrNotJSON = errors.New("galaxy: response was not JSON")

// bearer returns the token to attach, or "" when no Authorization header must
// be sent. An expired store never contributes a token, and neither does an
// empty one (galaxyapi.cpp:98-105): the request then goes out unauthenticated
// and the server's answer decides the outcome.
func (c *Client) bearer() string {
	if c.galaxy.IsExpired() {
		return ""
	}
	return c.galaxy.GetAccessToken()
}

// getResponse fetches target and returns the body (galaxyapi.cpp:94-133).
//
// Acceptance of encodings is left to the transport, which asks for gzip and
// decompresses it transparently. The C++ source sets CURLOPT_ACCEPT_ENCODING to
// "" for the same reason — curl negotiates and decompresses. Setting the header
// here would DISABLE Go's transparent decompression, so it is deliberately not
// touched.
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

// getResponseJSON fetches target and decodes the body as a JSON object
// (galaxyapi.cpp:135-188), including the zlib retry described on
// decodeJSONObject.
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
// (S14/S15/S17) work against a known object boundary.
//
// The zlib retry mirrors galaxyapi.cpp:146-180, and only that path: it is
// attempted strictly AFTER the first decode failed, and only when the first two
// bytes are a zlib stream header. Ordinary malformed JSON is never inflated, so
// its error stays a single ErrNotJSON. Inflation failures are not reported
// separately — the body was not a usable JSON object either way, and the first
// error describes what the caller asked for.
func decodeJSONObject(body string) (map[string]any, error) {
	obj, err := decodeObject(body)
	if err == nil {
		return obj, nil
	}
	if plain, ok := inflateZlibBody(body); ok {
		if obj, retryErr := decodeObject(string(plain)); retryErr == nil {
			return obj, nil
		}
	}
	return nil, err
}

// decodeObject decodes one JSON object body, with no compression handling.
func decodeObject(body string) (map[string]any, error) {
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

// inflateZlibBody inflates body when it starts with a zlib stream header, and
// reports whether it did.
//
// The header check is what limits the fallback to the compressed case. The
// C++ source reads the first two bytes as a little-endian uint16 and compares
// against 0x0178, 0x5e78, 0x9c78 and 0xda78 (galaxyapi.cpp:151-152), which
// byte-wise is 0x78 followed by 0x01, 0x5e, 0x9c or 0xda: a zlib stream with a
// 32 KiB window and the usual compression levels. The check is written here as
// two byte tests rather than through a shared uint16 reader, because nothing
// else needs one.
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
