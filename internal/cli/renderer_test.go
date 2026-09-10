package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/nekrozis/goggo/internal/transfer"
)

// TestRendererAggregatesAndPaints locks the D75/D79 renderer semantics: task
// starts register, progress updates fold into the view, finishes retire the
// line, messages print immediately, and a paint produces the frame the C++
// loop would — without the redraw loop running at all.
func TestRendererAggregatesAndPaints(t *testing.T) {
	var out bytes.Buffer
	r := newRenderer(&out, false, false, 0, time.Hour, func() int { return 120 })
	r.now = func() time.Time { return time.Unix(1_000_000, 0) }

	// The start order is deliberately not alphabetical: the #i numbering
	// follows the TaskStart arrival order (review D75), not a sorted view.
	r.OnEvent(transfer.Event{Path: "/b.bin", Kind: transfer.EventTaskStart})
	r.OnEvent(transfer.Event{Path: "/a.bin", Kind: transfer.EventTaskStart})
	r.OnEvent(transfer.Event{Path: "/a.bin", Text: "some message", Kind: transfer.EventMessageInfo})
	r.OnEvent(transfer.Event{Path: "/a.bin", Current: 50, Total: 100, Kind: transfer.EventProgress})
	r.OnEvent(transfer.Event{Path: "/b.bin", Current: 10, Total: 100, Kind: transfer.EventProgress})

	// The message printed immediately; no progress frame yet.
	if !strings.Contains(out.String(), "some message\n") {
		t.Errorf("output = %q, want the message before any paint", out.String())
	}

	r.paint()
	frame := out.String()
	for _, want := range []string{"#0 /b.bin", "#1 /a.bin", "50.00 B/100.00 B", "Remaining: 2"} {
		if !strings.Contains(frame, want) {
			t.Errorf("frame missing %q: %q", want, frame)
		}
	}

	// Finishing one task retires its line and drops the remaining count.
	r.OnEvent(transfer.Event{Path: "/a.bin", Kind: transfer.EventTaskFinish})
	out.Reset()
	r.paint()
	frame = out.String()
	if !strings.Contains(frame, "#1: Finished") || !strings.Contains(frame, "Remaining: 1") {
		t.Errorf("frame = %q, want the finished line and the lower count", frame)
	}

	// When every task has finished, no total line prints.
	r.OnEvent(transfer.Event{Path: "/b.bin", Kind: transfer.EventTaskFinish})
	out.Reset()
	r.paint()
	if strings.Contains(out.String(), "Remaining:") {
		t.Errorf("frame = %q, want no remaining line on a fully finished run", out.String())
	}
}

// TestRendererFoldAndWindowBehavior is not needed here: the fold lives in
// transfer; this file keeps the renderer's display semantics only. The window
// rate starts at zero, so the first frame shows no ETA.
func TestRendererFirstFrameHasNoETA(t *testing.T) {
	var out bytes.Buffer
	r := newRenderer(&out, false, false, 0, time.Hour, func() int { return 120 })
	r.now = func() time.Time { return time.Unix(1_000_000, 0) }

	r.OnEvent(transfer.Event{Path: "/a.bin", Kind: transfer.EventTaskStart})
	r.OnEvent(transfer.Event{Path: "/a.bin", Current: 5, Total: 100, Kind: transfer.EventProgress})
	r.paint()
	if strings.Contains(out.String(), "ETA:") {
		t.Errorf("frame = %q, want no ETA before the window has a slope", out.String())
	}
}

// TestRateWindowTrimsByTime locks the D76 window's two trim conditions: the
// slope spans at most the last 10 seconds and at most 100 points.
func TestRateWindowTrimsByTime(t *testing.T) {
	var w rateWindow
	w.cap = 100
	t0 := time.Unix(1_000_000, 0)
	// An old sample, then two fresh ones 10s apart from each other.
	w.add(t0.Add(-60*time.Second), 0)
	w.add(t0, 100)
	w.add(t0.Add(10*time.Second), 300)
	if got := w.rate(t0.Add(10 * time.Second)); got != 20 {
		t.Errorf("rate = %v, want the slope over the last 10s only (20 B/s)", got)
	}
	// The 100-point cap still applies inside the window.
	for i := 1; i <= 200; i++ {
		w.add(t0.Add(10*time.Second+time.Duration(i)*time.Millisecond), 300)
	}
	if len(w.points) > 100 {
		t.Errorf("points = %d, want at most 100", len(w.points))
	}
	// A stall past the window zeroes the slope at query time — the stale
	// prefix must not keep feeding the ETA.
	if got := w.rate(t0.Add(60 * time.Second)); got != 0 {
		t.Errorf("rate after a 50s stall = %v, want 0", got)
	}
}

// TestRendererSeparatesAvgFromInstantaneous locks the two D76 rates: the
// displayed @ rate is the session average, while the instantaneous rate stays
// the window slope the ETA is derived from.
func TestRendererSeparatesAvgFromInstantaneous(t *testing.T) {
	var out bytes.Buffer
	r := newRenderer(&out, false, false, 0, time.Hour, func() int { return 120 })
	base := time.Unix(1_000_000, 0)
	r.now = func() time.Time { return base }

	r.OnEvent(transfer.Event{Path: "/a.bin", Kind: transfer.EventTaskStart})
	if r.avgRate() != 0 {
		t.Errorf("avg before Start = %v, want 0", r.avgRate())
	}
	r.Start()
	// Five seconds in, 50 bytes installed: the session average is 10 B/s,
	// while the window holds a single point and has no slope yet.
	r.now = func() time.Time { return base.Add(5 * time.Second) }
	r.OnEvent(transfer.Event{Path: "/a.bin", Current: 50, Total: 100, Kind: transfer.EventProgress})
	if got := r.avgRate(); got != 10 {
		t.Errorf("avg = %v, want 10 B/s", got)
	}
	if got := r.window.rate(r.now()); got != 0 {
		t.Errorf("instantaneous = %v, want 0 with a single window point", got)
	}
	out.Reset()
	r.paint()
	if !strings.Contains(out.String(), "@ 0.01KiB/s") { // 10 B/s as the util formats it
		t.Errorf("frame = %q, want the session average as the displayed rate", out.String())
	}
	r.Stop()
}
