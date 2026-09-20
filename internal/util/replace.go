package util

import "strings"

// ReplaceOnce replaces the first occurrence of old with new and reports whether
// anything changed.
func ReplaceOnce(s, old, new string) (string, bool) {
	if old == "" {
		// An empty "old" is never used by callers, so treat it as a no-op.
		return s, false
	}
	idx := strings.Index(s, old)
	if idx < 0 {
		return s, false
	}
	return s[:idx] + new + s[idx+len(old):], true
}

// ReplaceAll replaces every non-overlapping occurrence of old in s and reports
// whether anything changed. Replaced segments are not re-scanned, so a
// replacement that itself contains old is left alone. An empty old is a no-op.
func ReplaceAll(s, old, new string) (string, bool) {
	if old == "" {
		return s, false
	}
	out := strings.ReplaceAll(s, old, new)
	return out, out != s
}
