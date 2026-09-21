package util

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// strippedAllowed reports whether a byte survives StrippedString: an ASCII
// letter or digit, the space, or one of '-', '_', '.', '(', ')', '[', ']', '{',
// '}'. The rule is pinned to these exact bytes; unicode helpers must NOT be used
// as a substitute.
func strippedAllowed(c byte) bool {
	switch {
	case c == ' ':
		return true
	case c >= '0' && c <= '9':
		return true
	case c >= 'A' && c <= 'Z':
		return true
	case c >= 'a' && c <= 'z':
		return true
	}
	switch c {
	case '-', '_', '.', '(', ')', '[', ']', '{', '}':
		return true
	}
	return false
}

// StrippedString is a byte-level filter: it keeps ASCII [A-Za-z0-9], the space
// and -_.[]{} and drops every other byte (so UTF-8 multi-byte sequences are
// removed byte-by-byte).
func StrippedString(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if strippedAllowed(s[i]) {
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// ManualURLsFromJSON collects every "manualUrl" value reachable from v: a
// "manualUrl" member is taken without recursing into it, any other value is walked.
//
// Ordering: JSON arrays keep their element order, while object members are visited
// in sorted key order, because Go's map[string]any does not preserve document order
// and the result must be deterministic.
func ManualURLsFromJSON(v any) ([]string, error) {
	var urls []string
	if err := collectManualURLs(v, &urls); err != nil {
		return nil, err
	}
	return urls, nil
}

// collectManualURLs is the recursive core of ManualURLsFromJSON. A value that is
// neither an array nor an object contributes nothing.
func collectManualURLs(v any, urls *[]string) error {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if k == "manualUrl" {
				s, err := manualURLString(t[k])
				if err != nil {
					return fmt.Errorf("util: manualUrl: %w", err)
				}
				*urls = append(*urls, s)
				continue
			}
			if err := collectManualURLs(t[k], urls); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range t {
			if err := collectManualURLs(child, urls); err != nil {
				return err
			}
		}
	}
	return nil
}

func manualURLString(v any) (string, error) {
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
	case map[string]any:
		return "", fmt.Errorf("expected a string, got object")
	case []any:
		return "", fmt.Errorf("expected a string, got array")
	default:
		return "", fmt.Errorf("expected a string, got %T", v)
	}
}

// dlcURLPrefix is the marker DLCNamesFromJSON keys on.
const dlcURLPrefix = "/downloads/"

// DLCNamesFromJSON extracts the distinct DLC names referenced by a game's
// "dlcs" subtree: for every manual URL that contains
// "/downloads/", the segment between that marker and the LAST '/' of the URL
// is a DLC name, de-duplicated with the first occurrence winning.
//
// A URL whose "/downloads/" marker has no following '/' is skipped.
func DLCNamesFromJSON(v any) ([]string, error) {
	urls, err := ManualURLsFromJSON(v)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, u := range urls {
		i := strings.Index(u, dlcURLPrefix)
		if i < 0 {
			continue
		}
		start := i + len(dlcURLPrefix)
		end := strings.LastIndex(u, "/")
		if end < start {
			continue
		}
		name := u[start:end]
		if !containsString(names, name) {
			names = append(names, name)
		}
	}
	return names, nil
}

// containsString reports whether want is already in list.
func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
