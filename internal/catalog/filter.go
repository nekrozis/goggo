package catalog

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// Filters holds the compiled name filters of one listing run (website.cpp:143-166).
//
// Fields are ordered to minimise padding: the slice (24B) first, then the
// pointer (8B).
type Filters struct {
	// Games is the game name filter list. C++ builds it from --game-regex when
	// that is set, otherwise from the lines of --game-list-file (the two are
	// mutually exclusive, matching the if/else-if in the original).
	Games []*regexp.Regexp

	// IgnoreDLCCount matches game names whose DLC information is fetched even
	// when the product reports no DLCs.
	IgnoreDLCCount *regexp.Regexp
}

// LoadFilterList reads a game filter list file: one regular expression per
// non-empty line (website.cpp:149-166).
//
// Differences from the C++ reader (intentional): a trailing CR is stripped so
// files written on Windows work, and an unreadable file is an error instead of
// a printed message followed by a run without filters (library layers never
// print or silently degrade).
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
// sources are compiled BEFORE any request is made (review lock, D), so an
// invalid pattern cannot surface after several pages have already been
// fetched. The C++ source constructs boost::regex lazily and would terminate
// the process on a malformed pattern.
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
// substring match, mirroring boost::regex_search (not regex_match).
//
// Note (review lock, D4): the patterns are evaluated by RE2, which does not
// support backreferences or lookaround; those spellings are a compile error
// rather than a silently different match.
func MatchesAny(res []*regexp.Regexp, name string) bool {
	for _, re := range res {
		if re.MatchString(name) {
			return true
		}
	}
	return false
}
