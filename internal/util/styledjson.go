package util

import (
	"bytes"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"errors"
	"io"
	"strings"
)

// styledIndent is the indentation every styled document uses.
const styledIndent = "\t"

// trimJSONSpace drops the whitespace JSON allows around a value — space, tab, CR
// and LF, and nothing else. The four bytes are listed rather than delegated to a
// Unicode whitespace trim, which would also swallow characters JSON does not
// accept there and so would turn an invalid document into an accepted one.
func trimJSONSpace(b []byte) []byte {
	for len(b) > 0 {
		switch b[0] {
		case ' ', '\t', '\r', '\n':
			b = b[1:]
		default:
			return b
		}
	}
	return b
}

// WriteStyledJSON marshals v onto w in the style every stored or printed JSON
// document uses: tab indentation, deterministic map ordering, no HTML escaping,
// and a trailing newline.
//
// Deterministic is required rather than decorative: without it the encoder
// orders a Go map's members differently on every run, and a saved artifact stops
// being reproducible.
//
// A nil slice marshals as [] and a nil map as {}, not as null.
func WriteStyledJSON(w io.Writer, v any) error {
	if err := jsonv2.MarshalWrite(w, v,
		jsontext.WithIndent(styledIndent),
		jsonv2.Deterministic(true),
		jsontext.EscapeForHTML(false),
	); err != nil {
		return err
	}
	// MarshalWrite leaves the document unterminated; the output contract has a
	// trailing newline.
	_, err := io.WriteString(w, "\n")
	return err
}

// StyledJSON renders v as the same styled text, minus the trailing newline —
// the form stored inside documents and artifact strings.
func StyledJSON(v any) (string, error) {
	var b bytes.Buffer
	if err := WriteStyledJSON(&b, v); err != nil {
		return "", err
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// WriteStyledJSONBytes reformats raw — an already-encoded JSON document — in the
// same style WITHOUT decoding it into Go values: member order, number literals
// and string escape spellings come out as they went in, and only the whitespace
// is regenerated.
//
// It accepts exactly one complete top-level value surrounded by nothing but
// whitespace, and rejects a second value, trailing garbage, malformed JSON, a
// duplicate object member name and invalid UTF-8.
//
// Keep this separate from WriteStyledJSON: routing a document through a Go value
// is exactly what would lose those literals.
func WriteStyledJSONBytes(w io.Writer, raw []byte) error {
	// Only the emptiness of the surrounding JSON whitespace is decided here; the
	// formatter is handed the ORIGINAL bytes. That matters: whitespace which is
	// not one of JSON's four characters (a U+00A0 prefix, say) must reach the
	// formatter and be rejected, and trimming it away first would accept a
	// document the input boundary is supposed to refuse.
	if len(trimJSONSpace(raw)) == 0 {
		return errors.New("styled JSON: empty input")
	}
	// AppendFormat validates and formats one complete value in one step and
	// rejects anything after it, so no separate top-level-value check is needed.
	// On an error it reports the original input rather than a partial result.
	//
	// PreserveRawStrings is the load-bearing option: without it a string is
	// decoded and re-escaped, and the document's bytes change.
	out, err := jsontext.AppendFormat(nil, raw,
		jsontext.Multiline(true),
		jsontext.WithIndent(styledIndent),
		jsontext.PreserveRawStrings(true),
		jsontext.CanonicalizeRawInts(false),
		jsontext.CanonicalizeRawFloats(false),
		jsontext.ReorderRawObjects(false),
		jsontext.AllowDuplicateNames(false),
		jsontext.AllowInvalidUTF8(false),
		jsontext.EscapeForHTML(false),
	)
	if err != nil {
		return err
	}
	if _, err := w.Write(out); err != nil {
		return err
	}
	// AppendFormat omits the top-level newline.
	_, err = io.WriteString(w, "\n")
	return err
}

// StyledJSONBytes renders raw as the same styled text, minus the trailing
// newline. See WriteStyledJSONBytes for the accepted input.
func StyledJSONBytes(raw []byte) (string, error) {
	var b bytes.Buffer
	if err := WriteStyledJSONBytes(&b, raw); err != nil {
		return "", err
	}
	return strings.TrimRight(b.String(), "\n"), nil
}
