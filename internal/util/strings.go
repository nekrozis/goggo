package util

import (
	"bytes"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
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
// in sorted key order. The walk gathers a set-like result, so it must not depend on
// how the document happened to order an object's members.
func ManualURLsFromJSON(v jsontext.Value) ([]string, error) {
	var urls []string
	if err := collectManualURLs(v, &urls); err != nil {
		return nil, err
	}
	return urls, nil
}

// collectManualURLs is the recursive core of ManualURLsFromJSON. A value that is
// neither an array nor an object contributes nothing.
func collectManualURLs(v jsontext.Value, urls *[]string) error {
	switch v.Kind() {
	case jsontext.KindBeginObject:
		var members map[string]jsontext.Value
		if err := jsonv2.Unmarshal(v, &members); err != nil {
			return err
		}
		keys := make([]string, 0, len(members))
		for k := range members {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if k == "manualUrl" {
				s, err := manualURLString(members[k])
				if err != nil {
					return fmt.Errorf("util: manualUrl: %w", err)
				}
				*urls = append(*urls, s)
				continue
			}
			if err := collectManualURLs(members[k], urls); err != nil {
				return err
			}
		}
	case jsontext.KindBeginArray:
		var elements []jsontext.Value
		if err := jsonv2.Unmarshal(v, &elements); err != nil {
			return err
		}
		for _, child := range elements {
			if err := collectManualURLs(child, urls); err != nil {
				return err
			}
		}
	}
	return nil
}

// manualURLString reads a loosely typed manualUrl member, formatting numbers as
// fixed-point float64. Containers return an error.
func manualURLString(v jsontext.Value) (string, error) {
	switch v.Kind() {
	case jsontext.KindInvalid, jsontext.KindNull:
		return "", nil
	case jsontext.KindString, jsontext.KindNumber, jsontext.KindTrue, jsontext.KindFalse:
		tok, err := jsontext.NewDecoder(bytes.NewReader(v)).ReadToken()
		if err != nil {
			return "", err
		}
		if tok.Kind() == jsontext.KindNumber {
			f, err := tok.Float()
			if err != nil {
				return "", err
			}
			return strconv.FormatFloat(f, 'f', -1, 64), nil
		}
		return tok.String(), nil
	case jsontext.KindBeginObject:
		return "", fmt.Errorf("expected a string, got object")
	case jsontext.KindBeginArray:
		return "", fmt.Errorf("expected a string, got array")
	}
	// Value.Kind reports neither an end-object nor an end-array, so nothing else
	// reaches here; the branch keeps the function total.
	return "", fmt.Errorf("expected a string, got %s", v.Kind())
}

// dlcURLPrefix is the marker DLCNamesFromJSON keys on.
const dlcURLPrefix = "/downloads/"

// DLCNamesFromJSON extracts the distinct DLC names referenced by a game's
// "dlcs" subtree: for every manual URL that contains
// "/downloads/", the segment between that marker and the LAST '/' of the URL
// is a DLC name, de-duplicated with the first occurrence winning.
//
// A URL whose "/downloads/" marker has no following '/' is skipped.
func DLCNamesFromJSON(v jsontext.Value) ([]string, error) {
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
