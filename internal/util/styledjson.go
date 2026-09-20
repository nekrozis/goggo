package util

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
)

// WriteStyledJSON encodes v onto w in the project's stable JSON style: tab
// indentation and no HTML escaping. It is the single writer seam for every
// stored or printed JSON document.
func WriteStyledJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "\t")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// StyledJSON renders v as the same styled text, minus the encoder's trailing
// newline — the form stored inside documents and artifact strings.
func StyledJSON(v any) (string, error) {
	var b bytes.Buffer
	if err := WriteStyledJSON(&b, v); err != nil {
		return "", err
	}
	return strings.TrimRight(b.String(), "\n"), nil
}
