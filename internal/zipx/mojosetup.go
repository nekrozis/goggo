package zipx

import (
	"bytes"
	"regexp"
	"strconv"
)

// MojoSetup markers found in the shell-script preamble of MojoSetup self-extracting
// installers: the offset line declares how many leading lines of the file form the
// script (consumed by `head -n N "$0"`), the filesizes line the inner archive byte
// count. \x60 is the literal backtick.
var (
	mojoOffsetPattern = regexp.MustCompile(`(?i)offset=\x60head -n (\d+?) "\$0"`)
	mojoFilesizePat   = regexp.MustCompile(`(?i)filesizes="(\d+?)"`)
)

// MojoSetupScriptSize returns the byte length of the installer's leading shell
// script: N lines counted from the offset marker, each contributing its content
// length plus a trailing newline.
//
// API contract: the count is always taken from data[0], so callers with a non-zero
// cursor must slice data themselves. When the pattern does not match, zero is
// returned.
func MojoSetupScriptSize(data []byte) int64 {
	m := mojoOffsetPattern.FindSubmatch(data)
	if m == nil {
		return 0
	}
	// Out-of-range input maps to 0.
	n, err := strconv.ParseInt(string(m[1]), 10, 32)
	if err != nil {
		return 0
	}
	if n <= 0 {
		return 0
	}

	// Each counted line contributes its content plus one '\n', and the newlines
	// are folded into n, so only the content lengths of the first min(n, lines)
	// lines are summed; a trailing partial line still counts as one line.
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

// MojoSetupInstallerSize returns the declared inner installer size parsed from
// the filesizes marker. ok is false when the marker is absent or its value does
// not fit an int64.
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
