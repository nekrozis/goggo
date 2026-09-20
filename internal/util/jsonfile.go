package util

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
)

// ReadJSONFile reads and decodes a JSON document. Failures are returned with the
// file path attached rather than swallowed.
func ReadJSONFile(path string) (any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read JSON file %q: %w", path, err)
	}
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("parse JSON file %q: %w", path, err)
	}
	return v, nil
}

// JSONUintString renders a JSON value as an unsigned decimal string: a string
// value is returned verbatim; any other value is rendered as its unsigned
// decimal text when representable; otherwise "" is returned. Values arrive
// decoded (float64, json.Number, native ints).
func JSONUintString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	var u uint64
	switch n := v.(type) {
	case float64:
		if n < 0 || n != float64(uint64(n)) {
			return ""
		}
		u = uint64(n)
	case json.Number:
		i, err := n.Int64()
		if err != nil || i < 0 {
			return ""
		}
		u = uint64(i)
	case int:
		if n < 0 {
			return ""
		}
		u = uint64(n)
	case int64:
		if n < 0 {
			return ""
		}
		u = uint64(n)
	case uint64:
		u = n
	default:
		return ""
	}
	return strconv.FormatUint(u, 10)
}
