package galaxy

import (
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/nekrozis/goggo/internal/jsonval"
	"github.com/nekrozis/goggo/internal/util"
)

// GalaxyPathPlaceholder is the marker CdnURLTemplatesFromJSON leaves in a
// template where the caller has to insert the expanded galaxy path.
//
// It is a contract between this package and the download layer, not wire text:
// the API never sends it, and callers substitute it before a request is built.
// The API's url_format spells the path parameter "{path}", and this package
// keeps that spelling as the internal marker.
//
// The shared spelling is safe because the parameter pass replaces "{path}" with
// the parameter value plus the re-attached marker in ONE pass, so the final
// consumer's replacement only ever hits the re-attached marker. Should a
// url_format carry "{path}" without a matching parameter, this package fills it
// with the galaxy path, which is the useful reading of that template.
const GalaxyPathPlaceholder = "{path}"

// CdnURLTemplatesFromJSON builds the ordered list of CDN URL templates from a
// link document.
//
// Every entry of "urls" contributes one template. Its place in the result comes
// from the endpoint's rank in cdnPriority: the index of the first match, or
// len(cdnPriority)+i, which puts an endpoint that is not listed behind every
// listed one and keeps unlisted endpoints in document order. The list is sorted
// by rank, lowest first, with a STABLE sort, so equally ranked endpoints (the
// same name twice) keep document order.
//
// Inside url_format every "{parameter}" is replaced by that member of
// "parameters". The members are visited in ascending key order, which is
// observable: a value may itself contain another parameter's placeholder.
//
// "{path}" is special: the marker is APPENDED to its value instead of replacing
// it, so the caller can fill in the path later. No normalisation is done here —
// no slash folding, no cleaning, no URL parsing.
//
// A document whose "urls" is missing or null produces no templates. A section
// that is present in another shape is reported, as elsewhere in this package: an
// entry that is not an object, a "parameters" that is not an object, or a scalar
// field of the wrong type. Missing or null scalar fields read as "".
func CdnURLTemplatesFromJSON(json map[string]any, cdnPriority []string) ([]string, error) {
	raw, ok := json["urls"]
	if !ok || raw == nil {
		return nil, nil
	}
	entries, err := jsonval.Array(raw)
	if err != nil {
		return nil, fmt.Errorf("galaxy: link document urls: %w", err)
	}

	type ranked struct {
		url   string
		score int
	}
	rankedURLs := make([]ranked, 0, len(entries))
	for i, element := range entries {
		entry, err := jsonval.Object(element)
		if err != nil {
			return nil, fmt.Errorf("galaxy: link document urls[%d]: %w", i, err)
		}
		name, err := jsonval.Str(entry["endpoint_name"])
		if err != nil {
			return nil, fmt.Errorf("galaxy: link document urls[%d].endpoint_name: %w", i, err)
		}
		target, err := urlTemplate(entry)
		if err != nil {
			return nil, fmt.Errorf("galaxy: link document urls[%d]: %w", i, err)
		}
		rankedURLs = append(rankedURLs, ranked{url: target, score: cdnRank(name, cdnPriority, i)})
	}

	sort.SliceStable(rankedURLs, func(a, b int) bool { return rankedURLs[a].score < rankedURLs[b].score })

	templates := make([]string, 0, len(rankedURLs))
	for _, entry := range rankedURLs {
		templates = append(templates, entry.url)
	}
	return templates, nil
}

// cdnRank scores an endpoint: its index in the configured priority, or
// len(cdnPriority)+index when it is not listed, which ranks it behind every
// listed endpoint.
func cdnRank(endpointName string, cdnPriority []string, index int) int {
	for i, name := range cdnPriority {
		if endpointName == name {
			return i
		}
	}
	return len(cdnPriority) + index
}

// urlTemplate renders one entry's url_format with its parameters applied.
//
// The keys are collected and sorted before the replacements run, because the
// order is observable when one parameter's value contains another parameter's
// placeholder.
func urlTemplate(entry map[string]any) (string, error) {
	format, err := jsonval.Str(entry["url_format"])
	if err != nil {
		return "", fmt.Errorf("url_format: %w", err)
	}

	raw, ok := entry["parameters"]
	if !ok || raw == nil {
		// A null parameters object means "nothing to replace".
		return format, nil
	}
	parameters, err := jsonval.Object(raw)
	if err != nil {
		return "", fmt.Errorf("parameters: %w", err)
	}

	keys := make([]string, 0, len(parameters))
	for name := range parameters {
		keys = append(keys, name)
	}
	sort.Strings(keys)

	for _, name := range keys {
		value, err := jsonval.Str(parameters[name])
		if err != nil {
			return "", fmt.Errorf("parameters[%q]: %w", name, err)
		}
		if name == "path" {
			value += GalaxyPathPlaceholder
		}
		format, _ = util.ReplaceAll(format, "{"+name+"}", value)
	}
	return format, nil
}

// PathFromDownlinkURL derives the depot-relative path from a downlink URL.
//
// The URL is percent-decoded, one trailing slash is removed, the path starts at
// the last "/<gamename>/" when that is present (otherwise after the last "/"),
// ends before the query string, and always carries the "/<gamename>/" prefix. A
// "?" after the last "/" means the URL format was unexpected, so the path is cut
// there; a path with no slash at all is left alone.
//
// It returns a string rather than an error because there is nothing here that can
// fail usefully: the input is a URL, not a document, so there is no shape to
// validate, and the purpose is to recover a path — the caller decides whether the
// result is usable (the download path rejects one that ends in "/secure").
//
// The corner cases are defined here:
//
//   - percent-decoding uses url.PathUnescape, which decodes %XX but does not turn
//     "+" into a space (QueryUnescape would). An invalid escape reports an error,
//     in which case the original text is kept.
//   - an empty URL skips the trailing-slash step; an empty result becomes
//     "/<gamename>/".
//   - an end position before the start position is clamped.
func PathFromDownlinkURL(downlinkURL, gameName string) string {
	decoded, err := url.PathUnescape(downlinkURL)
	if err != nil {
		decoded = downlinkURL
	}
	// Exactly one trailing slash: trimming every one of them would change the
	// result for an input that ends in several.
	if decoded != "" && strings.HasSuffix(decoded, "/") {
		decoded = decoded[:len(decoded)-1]
	}

	start := 0
	if idx := strings.LastIndexByte(decoded, '/'); idx >= 0 {
		start = idx + 1
	}
	if idx := strings.Index(decoded, "/"+gameName+"/"); idx >= 0 {
		start = idx
	}

	end := len(decoded)
	if q := strings.IndexByte(decoded, '?'); q >= 0 && q > start {
		end = q
		if strings.Contains(decoded, "?path=") {
			token := strings.Index(decoded, "&token=")
			accessToken := strings.Index(decoded, "&access_token=")
			switch {
			case token >= 0 && accessToken >= 0:
				end = min(token, accessToken)
			default:
				if amp := strings.IndexByte(decoded, '&'); amp >= 0 {
					end = amp
				}
			}
		}
	}
	if end < start {
		end = start
	}

	path := decoded[start:end]
	if !strings.Contains(path, "/"+gameName+"/") {
		path = "/" + gameName + "/" + path
	}

	// A "?" after the last "/" means the URL format was unexpected; a path with
	// no slash at all is not truncated.
	if q := strings.LastIndexByte(path, '?'); q >= 0 {
		if slash := strings.LastIndexByte(path, '/'); slash >= 0 && q > slash {
			path = path[:q]
		}
	}
	return path
}
