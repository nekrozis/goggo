package util

import "strings"

// NormalizeLanguage folds the spellings of one language token together: case is
// ignored and the two separators that appear on the wire are equivalent, so
// "ZH-hant" and "zh_Hant" name the same token as "zh-Hant".
//
// It is the comparison form, not a display form: a caller that reports a token
// back to the user reports the one the build declared.
func NormalizeLanguage(s string) string {
	return strings.ReplaceAll(strings.ToLower(s), "_", "-")
}

// LanguageFamilyMatch reports whether candidate answers a request: either the
// same token once normalized, or a member of the requested family.
//
// The prefix test runs on the normalized forms, so "en" answers "en-US" and
// "eng", and "zh" answers both "zh-Hans" and "zh-Hant". It is a bare prefix
// rather than a delimiter-bounded one on purpose: a manifest that declares
// "eng" or "english" must still answer the default request "en".
//
// It is the whole matching rule for a language request; the wildcard is not a
// language and is handled by the caller.
func LanguageFamilyMatch(request, candidate string) bool {
	r := NormalizeLanguage(request)
	if r == "" {
		return false
	}
	return strings.HasPrefix(NormalizeLanguage(candidate), r)
}
