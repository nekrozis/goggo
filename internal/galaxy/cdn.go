package galaxy

import (
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/nekrozis/goggo/internal/util"
)

// GalaxyPathPlaceholder is the marker CdnURLTemplatesFromJSON leaves in a
// template where the caller has to insert the expanded galaxy path.
//
// It is a contract between this package and the download layer, not wire text:
// the API never sends it. The API's url_format spells the path parameter
// "{path}", and keeping that spelling is safe because the parameter pass replaces
// "{path}" with the parameter value plus the re-attached marker in ONE pass, so
// the final consumer's replacement only ever hits the re-attached marker. A
// url_format carrying "{path}" with no matching parameter keeps the marker,
// which is the useful reading of that template.
const GalaxyPathPlaceholder = "{path}"

// CdnURLTemplatesFromJSON builds the ordered list of CDN URL templates from a
// link document.
//
// Every entry of "urls" contributes one template, ranked by the endpoint's
// position in cdnPriority: the index of the first match, or len(cdnPriority)+i
// for an unlisted endpoint, so unlisted endpoints sort behind every listed one in
// document order. The sort is stable, so equally ranked endpoints keep document
// order.
//
// Inside url_format every "{parameter}" is replaced by that member of
// "parameters", visited in ascending key order — observable, because a value may
// contain another parameter's placeholder. "{path}" is special: the marker is
// APPENDED to its value for the caller to fill in later. No normalisation is done
// here: no slash folding, no cleaning, no URL parsing.
//
// A document whose "urls" is missing or null produces no templates; a section
// present in another shape is reported, as elsewhere in this package.
func CdnURLTemplatesFromJSON(json map[string]any, cdnPriority []string) ([]string, error) {
	raw, ok := json["urls"]
	if !ok || raw == nil {
		return nil, nil
	}
	entries, err := mapArray(raw)
	if err != nil {
		return nil, fmt.Errorf("galaxy: link document urls: %w", err)
	}

	type ranked struct {
		url   string
		score int
	}
	rankedURLs := make([]ranked, 0, len(entries))
	for i, element := range entries {
		entry, err := mapObject(element)
		if err != nil {
			return nil, fmt.Errorf("galaxy: link document urls[%d]: %w", i, err)
		}
		name, err := scalarString(entry["endpoint_name"])
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
	format, err := scalarString(entry["url_format"])
	if err != nil {
		return "", fmt.Errorf("url_format: %w", err)
	}

	raw, ok := entry["parameters"]
	if !ok || raw == nil {
		// A null parameters object means "nothing to replace".
		return format, nil
	}
	parameters, err := mapObject(raw)
	if err != nil {
		return "", fmt.Errorf("parameters: %w", err)
	}

	keys := make([]string, 0, len(parameters))
	for name := range parameters {
		keys = append(keys, name)
	}
	sort.Strings(keys)

	for _, name := range keys {
		value, err := scalarString(parameters[name])
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

// PathFromDownlinkURL derives the depot-relative path from a downlink URL: the
// URL is percent-decoded, one trailing slash is removed, the path starts at the
// last "/<gamename>/" when that is present (otherwise after the last "/"), ends
// before the query string, and always carries the "/<gamename>/" prefix.
//
// It returns a string rather than an error because nothing here can fail
// usefully: the input is a URL, not a document, so there is no shape to validate,
// and the caller decides whether the result is usable (the download path rejects
// one that ends in "/secure").
//
// Two traps: percent-decoding uses url.PathUnescape, which decodes %XX but does
// not turn "+" into a space (QueryUnescape would) and keeps the original text on
// an invalid escape; and an end position before the start position is clamped.
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

func mapObject(v any) (map[string]any, error) {
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("expected a JSON object, got %s", mapKind(v))
	}
	return obj, nil
}

func mapArray(v any) ([]any, error) {
	arr, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("expected a JSON array, got %s", mapKind(v))
	}
	return arr, nil
}

func scalarString(v any) (string, error) {
	switch t := v.(type) {
	case nil:
		return "", nil
	case string:
		return t, nil
	case bool:
		if t {
			return "true", nil
		}
		return "false", nil
	case int:
		return strconv.Itoa(t), nil
	case int64:
		return strconv.FormatInt(t, 10), nil
	case uint64:
		return strconv.FormatUint(t, 10), nil
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64), nil
	default:
		return "", fmt.Errorf("expected a string, got %s", mapKind(v))
	}
}
