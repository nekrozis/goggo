package gogxml

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"strings"
)

// Parse decodes a GOG checksum XML document from r and validates its semantic rules.
//
// The whole input must be the document: after the root element only whitespace
// may follow, so a second root or trailing garbage is a syntax error rather
// than a silently ignored tail. This keeps the syntax/semantic split stable —
// everything past the root is classified before Validate is asked anything.
func Parse(r io.Reader) (*FileXML, error) {
	var f FileXML
	decoder := xml.NewDecoder(r)
	if err := decoder.Decode(&f); err != nil {
		return nil, err
	}
	if err := requireDocumentEnd(decoder); err != nil {
		return nil, err
	}
	if err := f.Validate(); err != nil {
		return nil, err
	}
	return &f, nil
}

// requireDocumentEnd consumes whatever follows the root element and accepts
// only whitespace, ending at the real end of input.
func requireDocumentEnd(decoder *xml.Decoder) error {
	for {
		tok, err := decoder.Token()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		switch t := tok.(type) {
		case xml.CharData:
			if strings.TrimSpace(string(t)) != "" {
				return errors.New("xml: unexpected content after the root element")
			}
		default:
			return errors.New("xml: unexpected content after the root element")
		}
	}
}

// Marshal formats f as formatted XML, prepending the standard XML declaration.
func Marshal(f *FileXML) ([]byte, error) {
	if err := f.Validate(); err != nil {
		return nil, err
	}
	body, err := xml.MarshalIndent(f, "", "  ")
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	buf.WriteString(xml.Header)
	buf.Write(body)
	buf.WriteString("\n")
	return buf.Bytes(), nil
}
