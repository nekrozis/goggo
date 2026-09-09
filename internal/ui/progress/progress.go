// Package progress renders download progress bars, ported from
// include/progressbar.h and src/progressbar.cpp of LGOGDownloader (WTFPL;
// pinned reference under /reference).
//
// Bar.Create is a pure computation; Bar.Draw writes it to a caller-supplied
// io.Writer (intentional API change: the C++ draw() writes to std::cout;
// output content and the "no trailing newline" semantics are unchanged).
//
// Scope guard (approved): this package does NOT do TTY detection, NO_COLOR,
// TERM handling, Windows ANSI setup or terminal-width queries — those belong
// to their own responsibilities (terminal-width work is planned for the CLI
// layer). color=true reproduces the exact C++ ANSI sequences; color=false
// emits no escape codes at all.
package progress

import (
	"io"
	"math"
)

// Block characters graded in eighths (index 0 is empty), matching the C++
// m_bar_chars (progressbar.cpp:17-28). Package-private on purpose.
var barChars = [...]string{
	" ",      // 0/8
	"\u258F", // 1/8 left one-eighth block
	"\u258E", // 2/8
	"\u258D", // 3/8
	"\u258C", // 4/8
	"\u258B", // 5/8
	"\u258A", // 6/8
	"\u2589", // 7/8
	"\u2588", // 8/8 full block
}

const (
	unicodeLeftBorder  = "\u2595" // right one-eighth block
	unicodeRightBorder = "\u258F" // left one-eighth block
	simpleLeftBorder   = "["
	simpleRightBorder  = "]"
	simpleEmptyFill    = " "
	simpleBarChar      = "="

	// ANSI colors (progressbar.cpp:35-38).
	ansiBarColor    = "\x1b[1;34m" // bold blue
	ansiBorderColor = "\x1b[1;37m" // bold white
	ansiReset       = "\x1b[0m"
)

// Bar draws an eighth-graded progress bar. C++ stores these settings once in
// the constructor; Go keeps them as the Bar value.
type Bar struct {
	useUnicode bool
	useColor   bool
}

// NewBar returns a Bar. useColor selects the exact ANSI coloring of the C++
// version; no terminal feature detection is performed (see package doc).
func NewBar(useUnicode, useColor bool) *Bar {
	return &Bar{useUnicode: useUnicode, useColor: useColor}
}

// minNormal is the smallest positive normal float64 (2^-1022). Anything
// smaller in magnitude (or zero) is a zero/subnormal value.
const minNormal = 2.2250738585072014e-308

// isCxxNormal reports whether v satisfies the specific std::isnormal
// semantics required by the original progressbar.cpp condition: finite,
// non-zero and not subnormal. It is a compatibility predicate, NOT a general
// floating-point helper for this codebase; keep it scoped to fraction
// clamping.
func isCxxNormal(v float64) bool {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return false
	}
	if v == 0 {
		return false
	}
	return math.Abs(v) >= minNormal
}

// clampFraction reproduces the C++ validation (progressbar.cpp:57-58):
//
//	if (!isnormal(fraction) || fraction < 0) fraction = 0;
//	else if (fraction > 1) fraction = 1;
//
// Note that !isnormal also catches zero and subnormal values (both signs),
// NaN and infinities — not only NaN/Inf.
func clampFraction(fraction float64) float64 {
	if !isCxxNormal(fraction) || fraction < 0 {
		return 0
	}
	if fraction > 1 {
		return 1
	}
	return fraction
}

// Create renders the bar of the given length. length must be >= 0; negative
// values are treated as 0 (the C++ signature takes an unsigned int, so this
// defensive behaviour guards the Go int API; see audit S07).
func (b *Bar) Create(length int, fraction float64) string {
	if length < 0 {
		length = 0
	}
	fraction = clampFraction(fraction)

	// C++ layout (progressbar.cpp:60-64): fraction*length full steps, plus a
	// partial character graded in eighths when the cursor is inside the bar.
	barPart := fraction * float64(length)
	whole := int(math.Floor(barPart))
	partialIndex := int(math.Floor((barPart - math.Floor(barPart)) * 8))

	leftBorder := simpleLeftBorder
	rightBorder := simpleRightBorder
	fullChar := simpleBarChar
	emptyChar := simpleEmptyFill
	partialChar := simpleEmptyFill
	if b.useUnicode {
		leftBorder = unicodeLeftBorder
		rightBorder = unicodeRightBorder
		fullChar = barChars[8]
		emptyChar = barChars[0]
		partialChar = barChars[partialIndex]
	}

	out := make([]byte, 0, length+8)
	if b.useColor {
		out = append(out, ansiBorderColor...)
	}
	out = append(out, leftBorder...)
	if b.useColor {
		out = append(out, ansiBarColor...)
	}
	for i := 0; i < whole; i++ {
		out = append(out, fullChar...)
	}
	if whole < length {
		out = append(out, partialChar...)
	}
	if b.useColor {
		out = append(out, ansiReset...)
	}
	for i := whole + 1; i < length; i++ {
		out = append(out, emptyChar...)
	}
	if b.useColor {
		out = append(out, ansiBorderColor...)
	}
	out = append(out, rightBorder...)
	if b.useColor {
		out = append(out, ansiReset...)
	}
	return string(out)
}

// Draw writes the rendered bar to w without a trailing newline (mirrors
// ProgressBar::draw, progressbar.cpp:48-51, but with an injected writer).
func (b *Bar) Draw(w io.Writer, length int, fraction float64) error {
	_, err := io.WriteString(w, b.Create(length, fraction))
	return err
}
