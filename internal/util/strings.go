package util

import (
	"fmt"
	"sort"
	"strings"

	"github.com/nekrozis/goggo/internal/jsonval"
)

// strippedAllowed reports whether a byte survives getStrippedString.
//
// C++ keeps a char when (isspace(c) && isprint(c)) || isalnum(c) or it is in
// {'-','_','.','(',')','[',']','{','}'}. On an unsigned char the only
// whitespace that is also printable is the space (0x20), and isalnum covers
// ASCII letters and digits only. The rule is therefore pinned to these exact
// bytes; unicode helpers must NOT be used as a substitute.
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

// StrippedString mirrors Util::getStrippedString (util.cpp:643-662) as a
// byte-level filter: it keeps ASCII [A-Za-z0-9], the space and -_.[]{}() and
// drops every other byte (so UTF-8 multi-byte sequences are removed
// byte-by-byte).
func StrippedString(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if strippedAllowed(s[i]) {
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// ManualURLsFromJSON collects every "manualUrl" value reachable from v,
// mirroring Util::getManualUrlsFromJSON (util.cpp:415-429): a "manualUrl"
// member is taken without recursing into it, any other value is walked.
//
// Ordering (review lock, O1): JSON arrays keep their element order, exactly as
// the C++ Json::Value iteration does. Go's map[string]any does not preserve the
// document order of object members, so object members are visited in sorted
// key order to keep the result deterministic. That affects only degenerate
// object-shaped responses, not the array shape the account API returns.
func ManualURLsFromJSON(v any) ([]string, error) {
	var urls []string
	if err := collectManualURLs(v, &urls); err != nil {
		return nil, err
	}
	return urls, nil
}

// collectManualURLs is the recursive core of ManualURLsFromJSON. A value that
// is neither an array nor an object contributes nothing, matching jsoncpp's
// `root.size() > 0` guard.
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
				s, err := jsonval.Str(t[k])
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

// dlcURLPrefix is the marker Util::getDLCNamesFromJSON keys on
// (util.cpp:442).
const dlcURLPrefix = "/downloads/"

// DLCNamesFromJSON extracts the distinct DLC names referenced by a game's
// "dlcs" subtree (util.cpp:431-459): for every manual URL that contains
// "/downloads/", the segment between that marker and the LAST '/' of the URL
// is a DLC name, de-duplicated with the first occurrence winning.
//
// A URL whose "/downloads/" marker has no following '/' is skipped; the C++
// source would form an invalid iterator range there (undefined behaviour), so
// skipping is the Go-side hardening.
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
