package webapi

import (
	"bytes"
	"fmt"

	"golang.org/x/net/html"
)

// extractInputValue locates the first <input> element whose name attribute
// equals wantName and returns its value attribute ("" when the attribute is
// absent). Only input name/value pairs are needed, and the HTML5 parser
// tolerates the real-world markup without an XHTML normalisation pass.
func extractInputValue(data []byte, wantName string) (string, error) {
	doc, err := html.Parse(bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("webapi: parse login form: %w", err)
	}
	value := ""
	found := false
	var walk func(*html.Node) bool
	walk = func(n *html.Node) bool {
		if n.Type == html.ElementNode && n.Data == "input" {
			var name, val string
			hasName := false
			for _, a := range n.Attr {
				switch a.Key {
				case "name":
					name = a.Val
					hasName = true
				case "value":
					val = a.Val
				}
			}
			if hasName && name == wantName {
				value = val
				found = true
				return true
			}
		}
		for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
			if walk(ch) {
				return true
			}
		}
		return false
	}
	walk(doc)
	if !found {
		return "", nil
	}
	return value, nil
}
