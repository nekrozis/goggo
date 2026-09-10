package webapi

import (
	"errors"
	"fmt"
	"math"
	"strconv"
)

// ErrNotJSON reports that a response body did not form the expected JSON
// document. It covers the SHAPE of a response only: HTTP failures surface as
// *httpx.StatusError and field-level problems are wrapped with context, so a
// caller can map ErrNotJSON onto its own hint — the C++ source prints
// "Response was not JSON. Cookies have most likely expired. Try --login
// first." (website.cpp:820).
var ErrNotJSON = errors.New("webapi: response was not JSON")

// The readers below mirror the jsoncpp accessors the original C++ relies on.
// They are deliberately narrow: each implements exactly one conversion the
// original performs, and returns an error where jsoncpp would fail the
// process. They must not grow into a general "best effort" coercion layer —
// per-field coercion belongs to the caller that needs it.
//
//	jsoncpp              reader
//	asString()           jsonStrLoose
//	asInt()              jsonIntStrict
//	isDouble()/asDouble() jsonNum / isJSONNumber
//	asBool()             jsonBool
//	isObject()/isArray() jsonObject / jsonArray
//	range-for            jsonChildren

// jsonKind names a decoded JSON value's type, for error messages.
func jsonKind(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case float64, int, int64, uint64:
		return "number"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return fmt.Sprintf("%T", v)
	}
}

// jsonObject requires a JSON object.
func jsonObject(v any) (map[string]any, error) {
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("expected a JSON object, got %s", jsonKind(v))
	}
	return obj, nil
}

// jsonArray requires a JSON array.
func jsonArray(v any) ([]any, error) {
	arr, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("expected a JSON array, got %s", jsonKind(v))
	}
	return arr, nil
}

// jsonChildren returns the values a jsoncpp range-for would visit: array
// elements in order, or the member values of an object. It is used where the
// original iterates a value without asserting its container kind
// (website.cpp:837).
func jsonChildren(v any) ([]any, error) {
	switch t := v.(type) {
	case []any:
		return t, nil
	case map[string]any:
		out := make([]any, 0, len(t))
		for _, e := range t {
			out = append(out, e)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("expected a JSON array or object, got %s", jsonKind(v))
	}
}

// jsonStrLoose mirrors jsoncpp's asString(): null becomes "", strings pass
// through, booleans and numbers are stringified. Arrays and objects have no
// string form in the original; they are errors here instead of terminating the
// process.
func jsonStrLoose(v any) (string, error) {
	switch t := v.(type) {
	case nil:
		return "", nil
	case string:
		return t, nil
	case bool:
		return strconv.FormatBool(t), nil
	case float64:
		return formatJSONNumber(t), nil
	case int:
		return strconv.Itoa(t), nil
	case int64:
		return strconv.FormatInt(t, 10), nil
	case uint64:
		return strconv.FormatUint(t, 10), nil
	default:
		return "", fmt.Errorf("expected a scalar, got %s", jsonKind(v))
	}
}

// formatJSONNumber renders a number the way jsoncpp stringifies a real value
// (%g with 17 significant digits), so integral values lose no information
// (123 -> "123"). NaN and infinities cannot appear in a decoded JSON document;
// they are formatted by Go's own rules if hand-built values inject them.
func formatJSONNumber(f float64) string {
	return strconv.FormatFloat(f, 'g', 17, 64)
}

// jsonMaxInt64Exclusive is 2^63 as a float64: the first value out of int64
// range. float64(math.MaxInt64) rounds up to exactly this value, so the
// comparison has to be >=.
const jsonMaxInt64Exclusive = float64(1 << 63)

// jsonIntStrict mirrors jsoncpp's asInt() for the integer shapes, but refuses
// values that would need a lossy conversion: a non-integral number is an error
// (jsoncpp truncates silently) because every caller treats these fields as
// integers. null is 0 and a boolean is 0/1, as in the original.
func jsonIntStrict(v any) (int64, error) {
	switch t := v.(type) {
	case nil:
		return 0, nil
	case bool:
		if t {
			return 1, nil
		}
		return 0, nil
	case int:
		return int64(t), nil
	case int64:
		return t, nil
	case uint64:
		if t > math.MaxInt64 {
			return 0, fmt.Errorf("integer %d overflows int64", t)
		}
		return int64(t), nil
	case float64:
		if math.IsNaN(t) || math.IsInf(t, 0) || t != math.Trunc(t) {
			return 0, fmt.Errorf("expected an integer, got %v", t)
		}
		if t >= jsonMaxInt64Exclusive || t < -jsonMaxInt64Exclusive {
			return 0, fmt.Errorf("integer %v overflows int64", t)
		}
		return int64(t), nil
	default:
		return 0, fmt.Errorf("expected an integer, got %s", jsonKind(v))
	}
}

// isJSONNumber mirrors isDouble(), which is true for integers as well as reals.
func isJSONNumber(v any) bool {
	switch v.(type) {
	case int, int64, uint64, float64:
		return true
	default:
		return false
	}
}

// jsonNum mirrors asDouble(): numbers, plus the conversions jsoncpp performs
// for null (0) and booleans (0/1). Strings are errors — callers that expect the
// original's `isDouble() ? number : asString()` split test isJSONNumber first
// and fall back to jsonStrLoose.
func jsonNum(v any) (float64, error) {
	switch t := v.(type) {
	case nil:
		return 0, nil
	case bool:
		if t {
			return 1, nil
		}
		return 0, nil
	case int:
		return float64(t), nil
	case int64:
		return float64(t), nil
	case uint64:
		return float64(t), nil
	case float64:
		return t, nil
	default:
		return 0, fmt.Errorf("expected a number, got %s", jsonKind(v))
	}
}

// jsonBool mirrors asBool(): booleans, null (false) and numbers (!= 0).
func jsonBool(v any) (bool, error) {
	switch t := v.(type) {
	case nil:
		return false, nil
	case bool:
		return t, nil
	case int:
		return t != 0, nil
	case int64:
		return t != 0, nil
	case uint64:
		return t != 0, nil
	case float64:
		return t != 0, nil
	default:
		return false, fmt.Errorf("expected a bool, got %s", jsonKind(v))
	}
}
