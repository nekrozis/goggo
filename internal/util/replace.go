package util

import "strings"

// ReplaceOnce mirrors Util::replaceString (util.cpp:354-363): it replaces
// the first occurrence of old with new. C++ mutates in place and returns
// 1/0; Go returns the new string and whether anything changed (intentional
// difference: immutable strings, bool instead of int).
func ReplaceOnce(s, old, new string) (string, bool) {
	if old == "" {
		// C++ find("") returns 0 and would insert at the front; an empty
		// "old" is never used by callers, so treat it as no-op here.
		return s, false
	}
	idx := strings.Index(s, old)
	if idx < 0 {
		return s, false
	}
	return s[:idx] + new + s[idx+len(old):], true
}

// ReplaceAll mirrors Util::replaceAllString (util.cpp:365-378) only in the
// common case. C++ re-searches from the start after every replacement, so a
// replacement that itself contains old is re-scanned (potentially forever);
// Go's ReplaceAll replaces non-overlapping occurrences of the original
// input. The Go semantics are intentionally adopted (intentional
// difference). An empty old is treated as no-op (C++ would loop forever).
func ReplaceAll(s, old, new string) (string, bool) {
	if old == "" {
		return s, false
	}
	out := strings.ReplaceAll(s, old, new)
	return out, out != s
}
