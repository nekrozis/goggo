package cli

import (
	"strings"
	"testing"

	"github.com/nekrozis/goggo/internal/ui/progress"
)

// TestVisibleWidth locks the cell arithmetic (review UI1 v3 §6.B): ANSI
// sequences are zero-width, East Asian wide runes are two cells, everything
// else one.
func TestVisibleWidth(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"plain ascii", "abc", 3},
		{"ansi color", "\x1b[1;34m[==]\x1b[0m", 4},
		{"wide cjk", "中文", 4},
		{"mixed", "a中b\x1b[0mc", 5},
		{"empty", "", 0},
	}
	for _, tc := range cases {
		if got := visibleWidth(tc.in); got != tc.want {
			t.Errorf("%s: visibleWidth(%q) = %d, want %d", tc.name, tc.in, got, tc.want)
		}
	}
}

// TestTruncateVisiblePreservesANSI locks that truncation cuts printable cells
// but never strands a color state: the reset sequence survives a cut.
func TestTruncateVisiblePreservesANSI(t *testing.T) {
	line := "\x1b[1;34m[========]\x1b[0m trailing text"
	got := truncateVisible(line, 6)
	if w := visibleWidth(got); w > 6 {
		t.Errorf("width = %d, want ≤ 6", w)
	}
	if !strings.HasPrefix(got, "\x1b[1;34m") {
		t.Errorf("got %q, want the opening sequence preserved", got)
	}
	if !strings.Contains(got, "\x1b[0m") {
		t.Errorf("got %q, want the reset sequence preserved", got)
	}
	// Wide runes are never split in half.
	if got := truncateVisible("中文中文中文", 5); visibleWidth(got) > 5 || got == "中文中文中文" {
		t.Errorf("got %q (width %d), want a whole-rune cut within 5 cells", got, visibleWidth(got))
	}
}

// TestLayoutNoWrapInvariant is the P0 guard (review UI1 v3 §6.B): with wide
// task paths, colored bars and a narrow terminal, every emitted line must fit
// the width budget — the coordinator's row arithmetic depends on the frame
// never wrapping.
func TestLayoutNoWrapInvariant(t *testing.T) {
	vm := viewModel{
		active: 3, queued: 150,
		rate: 2.5e6, remaining: 900 << 20, etaValid: true, etaSecs: 360,
		message: "Retry 1/3",
		tasks: []taskRow{
			{index: 1, path: "/install/dir/with/a/very/long/path/VIDEO.VID", pct: 0.58, done: 300 << 20, total: 577 << 20, rate: 350e3},
			{index: 2, path: "/install/dir/中文文件名特别长的游戏数据文件.BIN", pct: 0.81, done: 1 << 30, total: 1<<30 + 5, rate: 4.2e6},
			{index: 3, path: "/x", pct: 0.12, done: 15 << 20, total: 124 << 20, rate: 710e3},
		},
	}
	bar := progress.NewBar(true, true).Create
	for _, width := range []int{20, 40, 80, 120} {
		lines := layoutFrame(vm, width, 24, bar, 0)
		for i, line := range lines {
			if w := visibleWidth(line); w > width-1 {
				t.Errorf("width %d, line %d exceeds the budget: %d cells (%q)", width, i, w, line)
			}
		}
	}
}

// TestLayoutHeightTrim locks the height rule (review UI1 v3 §6.B): the frame
// is built first and the task rows are trimmed to the terminal height, so the
// physical row count never exceeds it and the overflow is announced.
func TestLayoutHeightTrim(t *testing.T) {
	vm := viewModel{active: 50, queued: 100, rate: 1e6, remaining: 5 << 20, etaValid: true, etaSecs: 5}
	for i := 0; i < 50; i++ {
		vm.tasks = append(vm.tasks, taskRow{index: i + 1, path: "/f.bin", pct: 0.5, done: 5, total: 10, rate: 1e3})
	}
	lines := layoutFrame(vm, 120, 12, nil, 0)
	if len(lines) > 11 {
		t.Errorf("rows = %d, want ≤ height−1 (11)", len(lines))
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "more") {
		t.Errorf("frame = %q, want the overflow notice", joined)
	}
}

// TestCompactPath locks the fish-style hierarchy-preserving compaction
// (review UI1-R2 §3): the basename is never sacrificed, leading directories
// abbreviate first, ancestors drop behind …/ only under heavier pressure,
// and a path that fits is never touched for tidiness.
func TestCompactPath(t *testing.T) {
	p := "Maps/Campaign/Shadow of Death/foo.h3m" // 38 cells; base foo.h3m = 8
	stages := []struct {
		cells int
		want  string
	}{
		{40, p}, // fits ⇒ untouched
		{35, "…/Campaign/Shadow of Death/foo.h3m"}, // three components
		{26, "…/Shadow of Death/foo.h3m"},          // deepest dir + name
		{8, "foo.h3m"},                             // last escape: bare name
		{6, "foo.h3"},                              // even that truncated
	}
	for _, st := range stages {
		if got := compactPath(p, st.cells); got != st.want {
			t.Errorf("compactPath(_, %d) = %q, want %q", st.cells, got, st.want)
		}
	}
	// Identity is never lost while context still fits: two same-named files
	// under different dirs compact to DIFFERENT forms (review UI1-R2 — the
	// basename is an escape hatch, not the norm).
	a := compactPath("Mods/Old/Data/Heroes3.exe", 20)
	b := compactPath("Mods/Old/Patch/Heroes3.exe", 20)
	if a != "…/Data/Heroes3.exe" || b != "…/Patch/Heroes3.exe" {
		t.Errorf("a/b = %q/%q, want dir-qualified forms kept distinct", a, b)
	}
	// A path that already fits is never rewritten for tidiness.
	if got := compactPath("Data/Heroes3.exe", 16); got != "Data/Heroes3.exe" {
		t.Errorf("fitting path = %q, want it untouched", got)
	}
}

// TestTaskLineDegradation locks the priority order (D31 + UI1-R2 §2): the bar
// goes first, then the path compacts hierarchically, then the byte counts,
// then the rate — the percentage and the row number survive everything short
// of a truncation, and compaction exists only for width.
func TestTaskLineDegradation(t *testing.T) {
	tk := taskRow{index: 7, path: "Data/Campaign/Shadow of Death/video.h3m", pct: 0.58, done: 300 << 20, total: 577 << 20, rate: 350e3}

	wide := taskLine(tk, 120, nil, 0)
	if !strings.Contains(wide, "Data/Campaign/Shadow of Death/video.h3m") || !strings.Contains(wide, "KiB/s") {
		t.Errorf("wide row = %q, want the full relative path, bytes and rate", wide)
	}
	mid := taskLine(tk, 60, nil, 0)
	if mid == wide && visibleWidth(wide) > 60 {
		t.Errorf("mid row = %q, want the path compacted under width pressure", mid)
	}
	if !strings.Contains(mid, "video.h3m") {
		t.Errorf("mid row = %q, want the basename kept", mid)
	}
	narrow := taskLine(tk, 17, nil, 0)
	if !strings.Contains(narrow, "58%") || !strings.Contains(narrow, "#7") {
		t.Errorf("narrow row = %q, want pct and row number to survive", narrow)
	}
	if w := visibleWidth(narrow); w > 17 {
		t.Errorf("narrow row width = %d, want ≤ 17", w)
	}
}

// TestLayoutTinyHeights locks the UI1-R1 fix: the height invariant holds at
// any terminal height, because summary, message, tasks and the overflow
// notice all draw from ONE row budget — at height 1–3 the frame shows only
// the highest-priority rows that fit instead of overflowing.
func TestLayoutTinyHeights(t *testing.T) {
	vm := viewModel{
		active: 3, queued: 100, rate: 1e6, remaining: 5 << 20, etaValid: true, etaSecs: 5,
		message: "Retry 1/3",
		tasks: []taskRow{
			{index: 1, path: "/a.bin", pct: 0.5, done: 5, total: 10, rate: 1e3},
			{index: 2, path: "/b.bin", pct: 0.5, done: 5, total: 10, rate: 1e3},
			{index: 3, path: "/c.bin", pct: 0.5, done: 5, total: 10, rate: 1e3},
		},
	}
	for _, height := range []int{1, 2, 3, 4, 5} {
		lines := layoutFrame(vm, 80, height, nil, 0)
		if len(lines) > height-1 {
			t.Errorf("height %d: rows = %d, want ≤ %d", height, len(lines), height-1)
		}
		for i, line := range lines {
			if w := visibleWidth(line); w > 79 {
				t.Errorf("height %d, line %d exceeds the width budget: %d", height, i, w)
			}
		}
	}
	// Height 4 (maxRows 3): the two summary rows and the message fill the
	// budget — no task row fits and there is no room for the overflow notice
	// either, so nothing may squeeze in.
	lines := layoutFrame(vm, 80, 4, nil, 0)
	if len(lines) != 3 || strings.Contains(strings.Join(lines, "\n"), "more") {
		t.Errorf("height 4 frame = %q, want exactly summary + message", lines)
	}
	// Height 5 (maxRows 4): the notice takes the only row left, and it must
	// count ALL hidden tasks — the reservation shrinks what fits, so the
	// count is computed from what is actually shown (review UI1-R1).
	lines = layoutFrame(vm, 80, 5, nil, 0)
	if len(lines) != 4 || !strings.Contains(lines[3], "... 3 more") {
		t.Errorf("height 5 frame = %q, want summary + message + '... 3 more'", lines)
	}
	// Height 6 (maxRows 5): one task row fits, and the notice counts the two
	// genuinely hidden ones.
	lines = layoutFrame(vm, 80, 6, nil, 0)
	if len(lines) != 5 || !strings.Contains(lines[3], "#1") || !strings.Contains(lines[4], "... 2 more") {
		t.Errorf("height 6 frame = %q, want summary + message + task #1 + '... 2 more'", lines)
	}
}
