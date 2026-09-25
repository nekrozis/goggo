package progress

import (
	"bytes"
	"math"
	"strings"
	"testing"
)

func TestFractionClampingMatchesCPlusPlus(t *testing.T) {
	smallestSubnormal := math.SmallestNonzeroFloat64 // ~5e-324
	cases := []struct {
		in   float64
		want float64
	}{
		{math.NaN(), 0},
		{math.Inf(1), 0},
		{math.Inf(-1), 0},
		{-1.0, 0},
		{-smallestSubnormal, 0},
		{0, 0},
		{smallestSubnormal, 0}, // positive subnormal: !isnormal -> 0
		{minNormal / 2, 0},     // subnormal
		{minNormal, minNormal}, // smallest normal: stays
		{0.5, 0.5},
		{1.0, 1.0},
		{1.0000001, 1.0},
		{2.0, 1.0},
	}
	for _, c := range cases {
		got := clampFraction(c.in)
		if (math.IsNaN(got) || got != c.want) && !(c.want == 0 && got == 0) {
			t.Errorf("clampFraction(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestSimpleBarBasics(t *testing.T) {
	b := NewBar(false, false)
	cases := []struct {
		length int
		frac   float64
		want   string
	}{
		{4, 0, "[    ]"},
		{4, 0.5, "[==  ]"},
		{4, 0.625, "[==  ]"}, // 2.5 whole=2 partial index 0 (empty) in simple mode
		{4, 1.0, "[====]"},
		{0, 0.5, "[]"},
		{-3, 0.5, "[]"}, // negative length treated defensively as 0
	}
	for _, c := range cases {
		if got := b.Create(c.length, c.frac); got != c.want {
			t.Errorf("simple Create(%d,%v) = %q, want %q", c.length, c.frac, got, c.want)
		}
	}
}

func TestUnicodePartialLadderLengthOne(t *testing.T) {
	b := NewBar(true, false)
	// For length=1 the partial index covers 0..7 as fraction goes 0..~1;
	// each output must stay exactly one inner character (no overflow).
	partialChar := func(idx int) string { return barChars[idx] }
	cases := []struct {
		frac float64
		want string
	}{
		{0.0, "\u2595 \u258f"},                        // idx 0 (space)
		{0.125, "\u2595" + partialChar(1) + "\u258f"}, // 1/8
		{0.25, "\u2595" + partialChar(2) + "\u258f"},  // 2/8
		{0.375, "\u2595" + partialChar(3) + "\u258f"},
		{0.5, "\u2595" + partialChar(4) + "\u258f"},
		{0.625, "\u2595" + partialChar(5) + "\u258f"},
		{0.75, "\u2595" + partialChar(6) + "\u258f"},
		{0.875, "\u2595" + partialChar(7) + "\u258f"},
		{0.9999999999999999, "\u2595" + partialChar(7) + "\u258f"},
		{1.0, "\u2595\u2588\u258f"}, // full block, no partial extra
	}
	for _, c := range cases {
		got := b.Create(1, c.frac)
		if got != c.want {
			t.Errorf("unicode Create(1,%v) = %q, want %q", c.frac, got, c.want)
		}
	}
}

func TestUnicodePartialIndexMidBar(t *testing.T) {
	b := NewBar(true, false)
	// 4 chars, fraction 0.625 -> 2.5: 2 full + 4/8 block + 1 empty.
	if got := b.Create(4, 0.625); got != "\u2595\u2588\u2588\u258c \u258f" {
		t.Errorf("unicode Create(4,0.625) = %q", got)
	}
	// Fraction just below 1 on length 4 must not exceed 4 inner chars.
	got := b.Create(4, 0.9999999999999999)
	if len([]rune(got)) != 6 { // border + 4 inner + border
		t.Errorf("near-1 Create length 4 runes = %d, want 6", len([]rune(got)))
	}
	if !strings.HasSuffix(got, "\u258f") || !strings.HasPrefix(got, "\u2595") {
		t.Errorf("near-1 Create = %q", got)
	}
}

// Contract (format): the coloured bar is consumed as bytes by the terminal and
// must match the C++ implementation's escape sequences exactly — the name of
// this test is the stated goal — so the assertion stays byte-exact.
func TestColorsMatchCPlusPlusSequences(t *testing.T) {
	simple := NewBar(false, true)
	if got := simple.Create(2, 0); got != "\x1b[1;37m[\x1b[1;34m \x1b[0m \x1b[1;37m]\x1b[0m" {
		t.Errorf("colored simple Create(2,0) = %q", got)
	}
	uni := NewBar(true, true)
	got := uni.Create(1, 1.0)
	want := "\x1b[1;37m\u2595\x1b[1;34m\u2588\x1b[0m\x1b[1;37m\u258f\x1b[0m"
	if got != want {
		t.Errorf("colored unicode Create(1,1) = %q, want %q", got, want)
	}
	// color=false must contain no escape bytes.
	if strings.Contains(NewBar(true, false).Create(3, 0.5), "\x1b") {
		t.Error("color=false output contains ANSI escapes")
	}
}

func TestDrawWritesWithoutNewline(t *testing.T) {
	b := NewBar(false, false)
	var buf bytes.Buffer
	if err := b.Draw(&buf, 4, 0.5); err != nil {
		t.Fatalf("Draw: %v", err)
	}
	if buf.String() != "[==  ]" {
		t.Errorf("Draw output = %q", buf.String())
	}
	if strings.Contains(buf.String(), "\n") {
		t.Error("Draw must not append a newline")
	}
}

// TestUnicodePartialIndexStaysInRange sweeps widths and fractions and
// asserts the rendered bar only ever contains glyphs the table can
// produce. The motivating case is arm64: the backend fuses the fraction
// subtraction into the preceding multiply, so 0.12 at width 75 (whose
// exact product rounds up to 9.0) yields a hair-negative difference that
// floors to -1 and panics barChars[-1] without the clamp. amd64 cannot
// reproduce the fusion, so the sweep pins the invariant on every
// platform and the macOS arm64 CI leg proves the fused path.
func TestUnicodePartialIndexStaysInRange(t *testing.T) {
	bar := NewBar(true, false)
	const allowed = "\u258F\u258E\u258D\u258C\u258B\u258A\u2589\u2588\u2595 "
	for length := range 81 {
		for i := range 101 {
			fraction := float64(i) / 100.0
			for _, r := range bar.Create(length, fraction) {
				if !strings.ContainsRune(allowed, r) {
					t.Fatalf("Create(%d, %v) emitted table-external glyph %q", length, fraction, r)
				}
			}
		}
	}
	// The exact arm64 trigger, pinned by name: no panic, borders intact.
	s := bar.Create(75, 0.12)
	if !strings.HasPrefix(s, "\u2595") || !strings.HasSuffix(s, "\u258F") {
		t.Errorf("Create(75, 0.12) = %q, want the unicode borders", s)
	}
}
