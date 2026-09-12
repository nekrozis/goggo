package cli

import (
	"fmt"
	"strings"

	"github.com/nekrozis/goggo/internal/util"
)

// visibleWidth counts the terminal cells a line occupies: ANSI escape
// sequences are zero-width, East Asian wide runes occupy two cells, everything
// else one. Width and content are measured in the same layer, so the layout's
// no-wrap invariant holds for colored and CJK output alike (review UI1 v3
// §6.B).
func visibleWidth(s string) int {
	width := 0
	inEscape := false
	for _, r := range s {
		if inEscape {
			// The first ASCII letter terminates a CSI sequence (\033[1;34m).
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				inEscape = false
			}
			continue
		}
		if r == '\x1b' {
			inEscape = true
			continue
		}
		width += runeCells(r)
	}
	return width
}

// runeCells reports the terminal cells one rune occupies.
func runeCells(r rune) int {
	switch {
	case r >= 0x1100 && r <= 0x115F, // Hangul Jamo
		r >= 0x2E80 && r <= 0xA4CF, // CJK radicals, CJK, Yi
		r >= 0xAC00 && r <= 0xD7A3, // Hangul syllables
		r >= 0xF900 && r <= 0xFAFF, // CJK compatibility ideographs
		r >= 0xFE30 && r <= 0xFE4F, // CJK compatibility forms
		r >= 0xFF00 && r <= 0xFF60, // fullwidth forms
		r >= 0xFFE0 && r <= 0xFFE6,
		r >= 0x20000 && r <= 0x3FFFD: // CJK extensions
		return 2
	}
	return 1
}

// truncateVisible shortens s so its visible width fits maxCells. Escape
// sequences are copied through untouched and never count against the budget.
// A cut inside a colored region would strand the terminal in that color, so a
// cut line that carries any escape sequence is closed with a reset — an extra
// reset after a closed state is harmless, a stranded color is not.
func truncateVisible(s string, maxCells int) string {
	if maxCells <= 0 {
		return ""
	}
	if visibleWidth(s) <= maxCells {
		return s
	}
	var b strings.Builder
	width := 0
	inEscape := false
	cut := false
	for _, r := range s {
		if inEscape {
			b.WriteRune(r)
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				inEscape = false
			}
			continue
		}
		if r == '\x1b' {
			inEscape = true
			b.WriteRune(r)
			continue
		}
		cells := runeCells(r)
		if width+cells > maxCells {
			cut = true
			break
		}
		b.WriteRune(r)
		width += cells
	}
	if cut && strings.Contains(b.String(), "\x1b[") {
		b.WriteString("\x1b[0m")
	}
	return b.String()
}

// layoutFrame composes the frame's physical lines from the view model: the
// two summary rows first, then the transient message row, then one row per
// active task and — when the tasks do not fit — an overflow notice.
//
// The hard invariant (review UI1 v3 §6.B): every returned line satisfies
// visibleWidth <= width-1, and the row count never exceeds height-1 (the one
// safety row keeps the last newline from scrolling, which would shift the
// coordinator's row arithmetic). ALL rows — summary, message, tasks and the
// overflow notice alike — draw from one budget, so the invariant holds at any
// terminal height; a terminal too small even for the summary simply shows the
// highest-priority rows that fit (review UI1-R1: the fixed rows used to be
// appended unconditionally and could overflow at height 1–3).
func layoutFrame(vm viewModel, width, height int, bar func(cells int, fraction float64) string, unit uint32) []string {
	maxCells := width - 1
	maxRows := height - 1
	if maxRows < 0 {
		maxRows = 0
	}
	add := func(lines []string, s string) []string {
		return append(lines, truncateVisible(s, maxCells))
	}

	var lines []string
	lines = add(lines, fmt.Sprintf("Downloading · %d active · %d queued", vm.active, vm.queued))
	summary := "Rate " + util.RateString(vm.rate, unit) +
		" · " + util.SizeString(uint64(vm.remaining), unit) + " remaining"
	if vm.etaValid {
		summary += " · ETA " + util.EtaString(int64(vm.etaSecs))
	}
	lines = add(lines, summary)
	if vm.message != "" {
		lines = add(lines, vm.message)
	}

	// The task rows get whatever height is left after the fixed rows. When
	// they do not fit, one row is reserved for the overflow notice — and the
	// notice's count must be computed from the tasks that will ACTUALLY be
	// shown, not from the pre-reservation budget: the reservation itself
	// shrinks what fits, and a notice that miscounts its own hidden rows is
	// worse than none (review UI1-R1).
	available := maxRows - len(lines)
	if available < 0 {
		available = 0
	}
	shown := len(vm.tasks)
	overflow := 0
	if shown > available {
		shown = max(available-1, 0) // one row goes to the notice
		overflow = len(vm.tasks) - shown
	}
	for _, t := range vm.tasks[:shown] {
		lines = add(lines, taskLine(t, maxCells, bar, unit))
	}
	if overflow > 0 && len(lines) < maxRows {
		lines = add(lines, fmt.Sprintf("... %d more", overflow))
	}
	// Enforce the invariant last, whatever the height: the frame's row count
	// is bounded by the budget and its lines by the width.
	if len(lines) > maxRows {
		lines = lines[:maxRows]
	}
	return lines
}

// taskLine renders one task row, degrading by priority (D31): the bar goes
// first, then the full path gives way to its base name, then the byte counts,
// then the rate. The percentage and the row number survive everything short
// of a truncation.
func taskLine(t taskRow, maxCells int, bar func(cells int, fraction float64) string, unit uint32) string {
	prefix := fmt.Sprintf("#%d ", t.index)
	pct := fmt.Sprintf("%3.0f%%", t.pct*100)
	bytes := util.SizeString(uint64(t.done), unit) + "/" + util.SizeString(uint64(t.total), unit)
	rate := util.RateString(t.rate, unit)

	base := prefix + t.path + " " + pct + " " + bytes + " " + rate
	// The bar is decoration: try to fit one into the space the base leaves.
	if bar != nil {
		free := maxCells - visibleWidth(base) - 1
		if free >= 5 {
			withBar := prefix + t.path + " " + bar(free, t.pct) + " " + pct + " " + bytes + " " + rate
			if visibleWidth(withBar) <= maxCells {
				return withBar
			}
		}
	}
	if visibleWidth(base) <= maxCells {
		return base
	}
	// Full path does not fit: the base name answers "which file" first.
	short := prefix + baseName(t.path) + " " + pct + " " + bytes + " " + rate
	if visibleWidth(short) <= maxCells {
		return short
	}
	noBytes := prefix + baseName(t.path) + " " + pct + " " + rate
	if visibleWidth(noBytes) <= maxCells {
		return noBytes
	}
	noRate := prefix + baseName(t.path) + " " + pct
	if visibleWidth(noRate) <= maxCells {
		return noRate
	}
	return truncateVisible(prefix+baseName(t.path), maxCells)
}

// baseName is the last path element — the file's own name.
func baseName(path string) string {
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		return path[i+1:]
	}
	return path
}
