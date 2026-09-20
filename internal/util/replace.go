package util

import "strings"

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
