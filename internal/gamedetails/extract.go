package gamedetails

import (
	"bytes"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"fmt"
	"regexp"
	"strings"
)

// This file holds the two extractions the save-* output face performs on the
// per-game details document. They are pure: the fetch belongs to the caller, the
// shapes belong here.

// brRE matches the "<br>" line-break markup, with optional horizontal
// whitespace and an optional closing slash. Matching is case-sensitive, so
// "<BR>" is not a line break.
var brRE = regexp.MustCompile(`<br[ \t]*/?>`)

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
func ChangelogFromJSON(raw []byte) (string, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return "", nil
	}
	var doc struct {
		Changelog jsontext.Value `json:"changelog"`
		Title     jsontext.Value `json:"title"`
	}
	if err := jsonv2.Unmarshal(raw, &doc); err != nil {
		return "", err
	}
	if len(doc.Changelog) == 0 {
		return "", nil
	}
	cTok, err := readToken(doc.Changelog)
	if err != nil {
		return "", err
	}
	if cTok.Kind() == jsontext.KindNull {
		return "", nil
	}
	if cTok.Kind() != jsontext.KindString {
		return "", fmt.Errorf("changelog: expected a JSON string, got %s", cTok.Kind())
	}
	changelog := cTok.String()
	if changelog == "" {
		return "", nil
	}
	title := "Changelog"
	if len(doc.Title) > 0 {
		tTok, err := readToken(doc.Title)
		if err != nil {
			return "", err
		}
		switch tTok.Kind() {
		case jsontext.KindNull:
			title = "Changelog: "
		case jsontext.KindString:
			title = "Changelog: " + tTok.String()
		default:
			return "", fmt.Errorf("title: expected a JSON string, got %s", tTok.Kind())
		}
	}
	return "<!DOCTYPE html>\n<html>\n<head>\n<meta charset=\"UTF-8\">\n<title>" +
		title + "</title>\n</head>\n<body>" + changelog + "</body>\n</html>", nil
}
