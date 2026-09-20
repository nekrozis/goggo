// Package progress renders download progress bars: an eighth-graded Unicode bar and
// a plain ASCII fallback, each with an optional ANSI color.
//
// Bar.Create is a pure computation; Bar.Draw writes it to a caller-supplied io.Writer
// and emits no trailing newline, and without useColor it emits no escape codes at all.
// The package does no terminal handling — no TTY detection, NO_COLOR, TERM handling,
// Windows ANSI setup or width queries; those belong to the front end.
package progress

import (
	"io"
	"math"
)

// barChars holds the block characters graded in eighths; index 0 is empty.
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

	// ANSI colors.
	ansiBarColor    = "\x1b[1;34m" // bold blue
	ansiBorderColor = "\x1b[1;37m" // bold white
	ansiReset       = "\x1b[0m"
)

// Bar draws a progress bar with the settings fixed at construction.
type Bar struct {
	useUnicode bool
	useColor   bool
}

// NewBar returns a Bar. useColor selects ANSI coloring; no terminal feature
// detection is performed (see the package doc).
func NewBar(useUnicode, useColor bool) *Bar {
	return &Bar{useUnicode: useUnicode, useColor: useColor}
}

// minNormal is the smallest positive normal float64 (2^-1022). Anything
// smaller in magnitude (or zero) is a zero/subnormal value.
const minNormal = 2.2250738585072014e-308

// isCxxNormal reports whether v is finite, non-zero and not subnormal — the
// predicate the fraction validation needs. It is not a general floating-point
// helper; keep it scoped to clampFraction.
func isCxxNormal(v float64) bool {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return false
	}
	if v == 0 {
		return false
	}
	return math.Abs(v) >= minNormal
}

// clampFraction maps a fraction outside [0, 1] to the nearest bound:
//
//	not normal (zero, subnormal, NaN, infinite) or negative → 0
//	greater than 1 → 1
//
// Note that the normality test catches zero and subnormal values (both signs) as
// well as NaN and infinities.
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
// values are treated as 0.
func (b *Bar) Create(length int, fraction float64) string {
	if length < 0 {
		length = 0
	}
	fraction = clampFraction(fraction)

	// The layout: fraction*length full steps, plus a partial character graded in
	// eighths when the cursor is inside the bar.
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

// Draw writes the rendered bar to w without a trailing newline.
func (b *Bar) Draw(w io.Writer, length int, fraction float64) error {
	_, err := io.WriteString(w, b.Create(length, fraction))
	return err
}
