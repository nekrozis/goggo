package gamedetails

import (
	"regexp"
	"strings"

	"github.com/nekrozis/goggo/internal/jsonval"
)

// This file ports the two extractions the save-* output face performs on the
// per-game details document (downloader.cpp:1607-1661). They are pure: the
// fetch belongs to the caller, the shapes belong here.

// brRE is the boost expression "<br\h*/?>" spelled for RE2, which has no \h
// (horizontal whitespace): [ \t]* is the same character class (GD5 9).
// Case follows boost's default - sensitive - so "<BR>" is not a line break.
var brRE = regexp.MustCompile("<br[ \\t]*/?>")

// SerialsFromCDKey ports getSerialsFromJSON's decidable half. A cdKey without
// <span> markup is plain text whose <br> tags become line breaks, terminated
// by a final newline (the C++ stream's endl). A cdKey carrying <span> markup
// needs the tidy normalizer upstream feeds it through - GD5 ruling 1 refuses
// to guess at that transformation: unsupported=true, no text, the caller
// warns and writes nothing (fail-closed, a compatibility boundary on record).
func SerialsFromCDKey(cdKey string) (text string, unsupported bool) {
	if cdKey == "" {
		return "", false
	}
	if strings.Contains(cdKey, "<span>") {
		return "", true
	}
	return brRE.ReplaceAllString(cdKey, "\n") + "\n", false
}

// ChangelogFromJSON ports getChangelogFromJSON: the raw changelog member
// wrapped into a standalone HTML document. The title reads "Changelog: <doc
// title>" when the document carries the member (present-but-empty included -
// the C++ isMember test, not a value test); an absent or empty changelog
// yields nothing. A member of the wrong shape is an error, never a coercion.
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
