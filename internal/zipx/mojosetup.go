package zipx

import (
	"bytes"
	"regexp"
	"strconv"
)

// MojoSetup markers found in the shell-script preamble of MojoSetup
// self-extracting installers. The offset line declares how many leading lines
// of the file form the script (consumed by `head -n N "$0"`); the filesizes
// line declares the inner archive byte count. Patterns mirror
// getMojoSetupScriptSize/getMojoSetupInstallerSize (ziputil.cpp:21,44):
// boost regex (perl | icase) is ported to RE2 with an inline (?i) flag and a
// \x60 escape for the literal backtick. Both engines return the LEFTMOST
// match, which is what the C++ regex_search semantics require.
var (
	mojoOffsetPattern = regexp.MustCompile(`(?i)offset=\x60head -n (\d+?) "\$0"`)
	mojoFilesizePat   = regexp.MustCompile(`(?i)filesizes="(\d+?)"`)
)

// MojoSetupScriptSize returns the byte length of the installer's leading
// shell script, mirroring getMojoSetupScriptSize (ziputil.cpp:17-39): it
// searches data for the offset marker and, on a match, counts N lines from
// data[0], each contributing len(line) plus the trailing newline that the
// C++ source appends.
//
// API contract: the count is always taken from data[0]. The C++ source reads
// lines from the current stream cursor; every known caller passes the full
// file from its start, so the two agree. Callers with a non-zero cursor must
// slice data themselves. When the regexp does not match, zero is returned
// (the C++ code leaves script_lines at zero and returns an empty script).
func MojoSetupScriptSize(data []byte) int64 {
	m := mojoOffsetPattern.FindSubmatch(data)
	if m == nil {
		return 0
	}
	// C++ parses the group with std::stoi (int). Out-of-range input aborts
	// there; it maps to 0 here (see the S06 OptionValue convention).
	n, err := strconv.ParseInt(string(m[1]), 10, 32)
	if err != nil {
		return 0
	}
	if n <= 0 {
		return 0
	}

	// Each counted line contributes len(line) bytes plus one '\n' (C++
	// appends line + "\n"). The '\n' for every counted line is folded into
	// n, so only the content lengths of the first min(n, lines) lines are
	// summed; a trailing partial line still counts as one getline success.
	var content int64
	rest := data
	for i := int64(0); i < n; i++ {
		idx := bytes.IndexByte(rest, '\n')
		if idx < 0 {
			content += int64(len(rest))
			break
		}
		content += int64(idx)
		rest = rest[idx+1:]
	}
	return n + content
}

// MojoSetupInstallerSize returns the declared inner installer size parsed
// from the filesizes marker, mirroring getMojoSetupInstallerSize
// (ziputil.cpp:41-53). ok is false when the marker is absent; this replaces
// the C++ -1 sentinel (see the S08a not-found convention). A filesizes value
// outside int64 also reports ok=false: std::stoll would abort there.
func MojoSetupInstallerSize(data []byte) (size int64, ok bool) {
	m := mojoFilesizePat.FindSubmatch(data)
	if m == nil {
		return 0, false
	}
	v, err := strconv.ParseInt(string(m[1]), 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}
