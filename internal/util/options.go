package util

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/nekrozis/goggo/internal/config"
)

var integerRE = regexp.MustCompile(`^[+-]?\d+$`)

// OptionValue resolves a command-line option value against options. The match
// order is fixed and must not be reordered:
//
//  1. str == "all" -> OR of every option ID
//  2. allowInt and an integer literal -> parsed with 32-bit bounds; an in-range
//     value is stored into uint32, so a negative literal wraps (e.g. -1 ->
//     0xFFFFFFFF); a literal outside the int32 range maps to 0
//  3. walk options in table order and for each entry:
//     a. if the entry has a non-empty Regexp and str matches it as a whole
//     (case-insensitive, anchored) -> that ID wins
//     b. otherwise if str == entry.Code -> that ID wins
//  4. no match -> 0
//
// Steps 3a/3b are evaluated per entry sequentially: an earlier entry's Code
// takes priority over a later entry's Regexp only through this order.
func OptionValue(str string, options []config.Option, allowInt bool) uint32 {
	if str == "all" {
		var value uint32
		for _, o := range options {
			value |= o.ID
		}
		return value
	}

	if allowInt && integerRE.MatchString(str) {
		// The literal is parsed with 32-bit bounds and stored into uint32, so
		// an in-range negative value wraps (e.g. -1 -> 0xFFFFFFFF). A literal
		// outside the int32 range maps to 0 (no match).
		n, err := strconv.ParseInt(str, 10, 32)
		if err != nil {
			return 0
		}
		return uint32(int32(n))
	}

	for _, o := range options {
		if o.Regexp != "" && matchOptionRegexp(str, o.Regexp) {
			return o.ID
		}
		if str == o.Code {
			return o.ID
		}
	}
	return 0
}

// matchOptionRegexp reports whether s fully matches rexp, case-insensitively,
// using an anchored "^(" + rexp + ")$" pattern.
func matchOptionRegexp(s, rexp string) bool {
	re, err := regexp.Compile("(?i)^(" + rexp + ")$")
	if err != nil {
		return false
	}
	return re.MatchString(s)
}

// OptionNameString collects the Name of every option whose ID is fully contained
// in value, preserving table order, joined with ", ".
func OptionNameString(value uint32, options []config.Option) string {
	var names []string
	for _, o := range options {
		if value&o.ID == o.ID {
			names = append(names, o.Name)
		}
	}
	return strings.Join(names, ", ")
}

// OptionByID returns the entry whose ID equals value exactly.
//
// A composite mask matches nothing, so the caller falls back to its own default.
// This is how the Galaxy language expression and the Galaxy architecture code
// are looked up.
func OptionByID(value uint32, options []config.Option) (config.Option, bool) {
	for _, o := range options {
		if o.ID == value {
			return o, true
		}
	}
	return config.Option{}, false
}

// ParseOptionString parses a priority expression: "," separates priority groups
// and "+" combines values inside one group.
//
// Duplicates: entries inside one "+" group collapse (the group value is the
// OR of its parts, so "a+a" yields a single "a"); duplicates across ","
// groups are preserved in the returned priority list ("a,a" yields [a, a]).
// The returned mask is the OR of all group values.
func ParseOptionString(s string, options []config.Option) (priority []uint32, mask uint32) {
	groups := Split(s, ",")
	for _, g := range groups {
		var value uint32
		for _, part := range Split(g, "+") {
			value |= OptionValue(part, options, true)
		}
		priority = append(priority, value)
		mask |= value
	}
	return priority, mask
}
