package util

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/nekrozis/goggo/internal/config"
)

var integerRE = regexp.MustCompile(`^[+-]?\d+$`)

// OptionValue mirrors Util::getOptionValue (util.cpp:513-549). The match
// order is fixed and must not be reordered:
//
//  1. str == "all"            -> OR of every option ID
//  2. allowInt and integer    -> parse with 32-bit bounds like std::stoi; an
//     in-range value is stored into uint32, so a negative literal wraps
//     (e.g. -1 -> 0xFFFFFFFF); an out-of-int32-range literal (C++: uncaught
//     std::out_of_range) maps to 0
//  3. walk options in table order and for each entry:
//     a. if the entry has a non-empty Regexp and str matches it as a whole
//     (case-insensitive, anchored) -> that ID wins
//     b. otherwise if str == entry.Code (string) -> that ID wins
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
		// C++ uses std::stoi (a 32-bit int) and assigns the result to an
		// unsigned int, so an in-range negative literal wraps (e.g. -1 ->
		// 0xFFFFFFFF). stoi throws std::out_of_range for literals outside
		// [-2147483648, 2147483647], which in the C++ program is an
		// uncaught abort; here that case maps to 0 (no match). ParseInt is
		// called with 32-bit bounds so behaviour follows std::stoi.
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
// mirroring the anchored "^(" + regexp + ")$" search of util.cpp:533.
func matchOptionRegexp(s, rexp string) bool {
	re, err := regexp.Compile("(?i)^(" + rexp + ")$")
	if err != nil {
		return false
	}
	return re.MatchString(s)
}

// OptionNameString mirrors Util::getOptionNameString (util.cpp:551-560): it
// collects the Name of every option whose ID is fully contained in value,
// preserving table order, joined with ", ".
func OptionNameString(value uint32, options []config.Option) string {
	var names []string
	for _, o := range options {
		if value&o.ID == o.ID {
			names = append(names, o.Name)
		}
	}
	return strings.Join(names, ", ")
}

// ParseOptionString mirrors Util::parseOptionString (util.cpp:563-579). The
// input uses "," to separate priority groups and "+" to combine values
// inside one group.
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
