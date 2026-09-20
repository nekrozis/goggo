package gamedetails

import (
	"regexp"
	"strings"

	"github.com/nekrozis/goggo/internal/jsonval"
)

// This file holds the two extractions the save-* output face performs on the
// per-game details document. They are pure: the fetch belongs to the caller, the
// shapes belong here.

// brRE matches the "<br>" line-break markup, with optional horizontal
// whitespace and an optional closing slash. Matching is case-sensitive, so
// "<BR>" is not a line break.
var brRE = regexp.MustCompile("<br[ \\t]*/?>")

// SerialsFromCDKey extracts the serials text from the cdKey member. A cdKey
// without <span> markup is plain text whose <br> tags become line breaks,
// terminated by a final newline. A cdKey carrying <span> markup would need the
// tidy normalizer this package does not implement, so it is reported as
// unsupported: no text, and the caller warns and writes nothing.
func SerialsFromCDKey(cdKey string) (text string, unsupported bool) {
	if cdKey == "" {
		return "", false
	}
	if strings.Contains(cdKey, "<span>") {
		return "", true
	}
	return brRE.ReplaceAllString(cdKey, "\n") + "\n", false
}

// ChangelogFromJSON wraps the raw changelog member into a standalone HTML
// document. The title reads "Changelog: <doc title>" when the document carries
// the member — present-but-empty counts — and plain "Changelog" otherwise; an
// absent or empty changelog yields nothing. A member of the wrong shape is an
// error, never a coercion.
func ChangelogFromJSON(doc map[string]any) (string, error) {
	raw, ok := doc["changelog"]
	if !ok || raw == nil {
		return "", nil
	}
	changelog, err := jsonval.Str(raw)
	if err != nil {
		return "", err
	}
	if changelog == "" {
		return "", nil
	}
	title := "Changelog"
	if rawTitle, has := doc["title"]; has {
		t, err := jsonval.Str(rawTitle)
		if err != nil {
			return "", err
		}
		title = "Changelog: " + t
	}
	return "<!DOCTYPE html>\n<html>\n<head>\n<meta charset=\"UTF-8\">\n<title>" +
		title + "</title>\n</head>\n<body>" + changelog + "</body>\n</html>", nil
}
