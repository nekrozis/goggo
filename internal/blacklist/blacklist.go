package blacklist

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"strings"
)

// item is one parsed blacklist line.
type item struct {
	linenr int
	source string
	re     *regexp.Regexp
}

// flag bits (blacklist.cpp:14-17).
const (
	flagRX   = 1 << 0
	flagPerl = 1 << 1
)

// Blacklist is a parsed set of path filters. The zero value is an empty list
// that matches nothing.
//
// The diagnostics Initialize produced stay on the value, so a caller that
// obtained it through LoadBlacklist can still render them.
type Blacklist struct {
	items       []item
	diagnostics []string
}

// Initialize parses the lines into filters and returns one diagnostic per
// problem line, in order. It never aborts: an unparsable or unknown-type line
// is reported and skipped, exactly as the C++ source does — except that the
// diagnostics are returned for the caller to render instead of printed here,
// and a line that carries flags but no expression is reported rather than
// crashing (upstream's substr runs past the end there, blacklist.cpp:43-56).
func (b *Blacklist) Initialize(lines []string) (diagnostics []string) {
	for nr, line := range lines {
		linenr := nr + 1
		// A file with CRLF endings leaves a '\r' on every line when split on
		// '\n', which would end up inside the expression and break '$'
		// anchors. Stripping it is an intentional difference, the same one the
		// catalog filter reader makes.
		line = strings.TrimSuffix(line, "\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		var flags int
		i := 0
		for ; i < len(line) && line[i] != ' '; i++ {
			switch line[i] {
			case 'R':
				flags |= flagRX
			case 'p':
				flags |= flagPerl
			default:
				diagnostics = append(diagnostics,
					fmt.Sprintf("unknown flag '%c' in blacklist line %d", line[i], linenr))
			}
		}
		i++ // skip the separator space

		if i >= len(line) {
			diagnostics = append(diagnostics, fmt.Sprintf("empty expression in blacklist line %d", linenr))
			continue
		}
		if flags&flagRX == 0 {
			diagnostics = append(diagnostics, fmt.Sprintf("unknown expression type in blacklist line %d", linenr))
			continue
		}

		source := line[i:]
		re, err := regexp.Compile(source)
		if err != nil {
			// Upstream assigns into a boost::regex, which throws on an invalid
			// pattern; here the line is reported and skipped.
			diagnostics = append(diagnostics, fmt.Sprintf("invalid regexp in blacklist line %d: %v", linenr, err))
			continue
		}
		b.items = append(b.items, item{linenr: linenr, source: source, re: re})
	}
	b.diagnostics = diagnostics
	return diagnostics
}

// IsBlacklisted reports whether path matches any entry (blacklist.cpp:65-72).
// The search is unanchored: an entry matches a path that contains it.
func (b *Blacklist) IsBlacklisted(path string) bool {
	for _, it := range b.items {
		if it.re.MatchString(path) {
			return true
		}
	}
	return false
}

// Diagnostics returns the problems Initialize reported, in file order. The
// library does not print, so this is how a caller that received the value from
// LoadBlacklist renders them.
func (b *Blacklist) Diagnostics() []string {
	return b.diagnostics
}

// LoadBlacklist reads the file at path and parses its lines. A missing file is
// the empty blacklist, which filters nothing — the state upstream is in when
// no blacklist.txt exists. Any other read error is returned: an unreadable
// file must not silently become "nothing is blacklisted". The parse
// diagnostics stay on the value (see Diagnostics).
func LoadBlacklist(path string) (*Blacklist, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return &Blacklist{}, nil
		}
		return nil, fmt.Errorf("blacklist: read %s: %w", path, err)
	}
	b := &Blacklist{}
	b.Initialize(strings.Split(string(data), "\n"))
	return b, nil
}
