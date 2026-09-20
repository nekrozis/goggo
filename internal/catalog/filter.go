package catalog

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// Filters holds the compiled name filters of one listing run.
type Filters struct {
	// Games is the game name filter list, built from --game-regex when that is
	// set, otherwise from the lines of --game-list-file. The two are mutually
	// exclusive.
	Games []*regexp.Regexp

	// IgnoreDLCCount matches game names whose DLC information is fetched even
	// when the product reports no DLCs.
	IgnoreDLCCount *regexp.Regexp
}

// LoadFilterList reads a game filter list file: one regular expression per
// non-empty line.
//
// A trailing CR is stripped so files written on Windows work, and an unreadable
// file is an error: this layer never prints or silently degrades.
func LoadFilterList(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("catalog: read game filter list %q: %w", path, err)
	}
	var lines []string
	for _, raw := range strings.Split(string(data), "\n") {
		if line := strings.TrimSuffix(raw, "\r"); line != "" {
			lines = append(lines, line)
		}
	}
	return lines, nil
}

// CompileFilters validates and compiles the filter configuration. All three
// sources are compiled before any request is made, so an invalid pattern cannot
// surface after several pages have already been fetched.
func CompileFilters(gameRegex, filterListPath, ignoreDLCCountRegex string) (Filters, error) {
	var f Filters
	switch {
	case gameRegex != "":
		re, err := regexp.Compile(gameRegex)
		if err != nil {
			return Filters{}, fmt.Errorf("catalog: --game-regex: %w", err)
		}
		f.Games = append(f.Games, re)
	case filterListPath != "":
		lines, err := LoadFilterList(filterListPath)
		if err != nil {
			return Filters{}, err
		}
		for i, line := range lines {
			re, err := regexp.Compile(line)
			if err != nil {
				return Filters{}, fmt.Errorf("catalog: filter list %q line %d: %w", filterListPath, i+1, err)
			}
			f.Games = append(f.Games, re)
		}
	}
	if ignoreDLCCountRegex != "" {
		re, err := regexp.Compile(ignoreDLCCountRegex)
		if err != nil {
			return Filters{}, fmt.Errorf("catalog: --ignore-dlc-count-regex: %w", err)
		}
		f.IgnoreDLCCount = re
	}
	return f, nil
}

// MatchesAny reports whether name matches any of the expressions. It is a
// substring match, not a whole-string match.
//
// The patterns are evaluated by RE2, which does not support backreferences or
// lookaround; those spellings are a compile error, not a silently different match.
func MatchesAny(res []*regexp.Regexp, name string) bool {
	for _, re := range res {
		if re.MatchString(name) {
			return true
		}
	}
	return false
}
