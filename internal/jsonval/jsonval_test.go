package jsonval

import (
	"math"
	"testing"
)

func TestJSONKindNames(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{nil, "null"},
		{true, "boolean"},
		{"s", "string"},
		{float64(1), "number"},
		{int64(1), "number"},
		{uint64(1), "number"},
		{[]any{}, "array"},
		{map[string]any{}, "object"},
	}
	for _, c := range cases {
		if got := Kind(c.in); got != c.want {
			t.Errorf("Kind(%#v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestJSONObjectAndArray(t *testing.T) {
	if _, err := Object(map[string]any{"a": 1}); err != nil {
		t.Errorf("Object(object) = %v, want nil", err)
	}
	if _, err := Object([]any{}); err == nil {
		t.Error("Object(array) = nil, want error")
	}
	if _, err := Array([]any{1}); err != nil {
		t.Errorf("Array(array) = %v, want nil", err)
	}
	if _, err := Array(map[string]any{}); err == nil {
		t.Error("Array(object) = nil, want error")
	}
}

func TestJSONChildren(t *testing.T) {
	arr, err := Children([]any{"a", "b"})
	if err != nil || len(arr) != 2 || arr[0] != "a" {
		t.Errorf("Children(array) = %v, %v", arr, err)
	}
	obj, err := Children(map[string]any{"k": "v"})
	if err != nil || len(obj) != 1 || obj[0] != "v" {
		t.Errorf("Children(object) = %v, %v", obj, err)
	}
	if _, err := Children("scalar"); err == nil {
		t.Error("Children(scalar) = nil, want error")
	}
	if _, err := Children(nil); err == nil {
		t.Error("Children(null) = nil, want error")
	}
}

// TestJSONStr mirrors jsoncpp's asString(): null/boolean/numbers stringify,
// containers do not.
func TestJSONStr(t *testing.T) {
	cases := []struct {
		name    string
		in      any
		want    string
		wantErr bool
	}{
		{"null", nil, "", false},
		{"string", "abc", "abc", false},
		{"empty string", "", "", false},
		{"true", true, "true", false},
		{"false", false, "false", false},
		{"integral float", float64(123), "123", false},
		{"real float", float64(12.5), "12.5", false},
		{"int", 42, "42", false},
		{"int64", int64(42), "42", false},
		{"uint64", uint64(42), "42", false},
		{"array", []any{1}, "", true},
		{"object", map[string]any{}, "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Str(c.in)
			if (err != nil) != c.wantErr {
				t.Fatalf("Str(%#v) err = %v, wantErr %v", c.in, err, c.wantErr)
			}
			if !c.wantErr && got != c.want {
				t.Errorf("Str(%#v) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestJSONInt covers the integer reader: jsoncpp's null/bool handling is kept,
// non-integral numbers and strings are refused instead of truncated.
func TestJSONInt(t *testing.T) {
	cases := []struct {
		name    string
		in      any
		want    int64
		wantErr bool
	}{
		{"null", nil, 0, false},
		{"true", true, 1, false},
		{"false", false, 0, false},
		{"int", 7, 7, false},
		{"int64", int64(7), 7, false},
		{"uint64", uint64(7), 7, false},
		{"uint64 overflow", uint64(math.MaxInt64) + 1, 0, true},
		{"integral float", float64(123), 123, false},
		{"negative integral float", float64(-9), -9, false},
		{"non-integral float", float64(1.5), 0, true},
		{"float 2^63", math.Ldexp(1, 63), 0, true},
		{"float min int64", -math.Ldexp(1, 63), math.MinInt64, false},
		{"below int64", -math.Ldexp(1, 64), 0, true},
		{"NaN", math.NaN(), 0, true},
		{"+Inf", math.Inf(1), 0, true},
		{"numeric string", "1", 0, true},
		{"array", []any{}, 0, true},
		{"object", map[string]any{}, 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Int(c.in)
			if (err != nil) != c.wantErr {
				t.Fatalf("Int(%#v) err = %v, wantErr %v", c.in, err, c.wantErr)
			}
			if !c.wantErr && got != c.want {
				t.Errorf("Int(%#v) = %d, want %d", c.in, got, c.want)
			}
		})
	}
}

// TestJSONNum mirrors asDouble(): numbers plus jsoncpp's null (0) and boolean
// (0/1) conversions; strings and containers are refused.
func TestJSONNum(t *testing.T) {
	cases := []struct {
		name    string
		in      any
		want    float64
		wantErr bool
	}{
		{"null", nil, 0, false},
		{"true", true, 1, false},
		{"false", false, 0, false},
		{"int", 3, 3, false},
		{"int64", int64(3), 3, false},
		{"uint64", uint64(3), 3, false},
		{"float", float64(1.5), 1.5, false},
		{"numeric string", "1.5", 0, true},
		{"array", []any{}, 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Num(c.in)
			if (err != nil) != c.wantErr {
				t.Fatalf("Num(%#v) err = %v, wantErr %v", c.in, err, c.wantErr)
			}
			if !c.wantErr && got != c.want {
				t.Errorf("Num(%#v) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

func TestIsJSONNumber(t *testing.T) {
	for _, in := range []any{float64(1), int(1), int64(1), uint64(1)} {
		if !IsNumber(in) {
			t.Errorf("IsNumber(%#v) = false, want true", in)
		}
	}
	for _, in := range []any{nil, "1", true, []any{}, map[string]any{}} {
		if IsNumber(in) {
			t.Errorf("IsNumber(%#v) = true, want false", in)
		}
	}
}

// TestJSONBool mirrors asBool(): null is false, numbers are != 0, strings are
// refused instead of parsed.
func TestJSONBool(t *testing.T) {
	cases := []struct {
		name    string
		in      any
		want    bool
		wantErr bool
	}{
		{"null", nil, false, false},
		{"true", true, true, false},
		{"false", false, false, false},
		{"int 1", 1, true, false},
		{"int 0", 0, false, false},
		{"float 0", float64(0), false, false},
		{"float 2", float64(2), true, false},
		{"string true", "true", false, true},
		{"array", []any{}, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Bool(c.in)
			if (err != nil) != c.wantErr {
				t.Fatalf("Bool(%#v) err = %v, wantErr %v", c.in, err, c.wantErr)
			}
			if !c.wantErr && got != c.want {
				t.Errorf("Bool(%#v) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}
