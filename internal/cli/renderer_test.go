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
// transfer; this file keeps the renderer's display semantics only. A task's
// first frame has no elapsed time behind its average, so no rate and no ETA
// print yet.
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

// TestRendererPerTaskRatesAndETAs locks the S-ETA1 semantics: each task's rate
// comes from its own window, the ETA and the displayed @ rate on a line are
// that same number, and the total rate is the sum of the running tasks' rates.
func TestRendererPerTaskRatesAndETAs(t *testing.T) {
	var out bytes.Buffer
	r := newRenderer(&out, false, false, 0, time.Hour, func() int { return 200 })
	base := time.Unix(1_000_000, 0)

	r.now = func() time.Time { return base }
	r.Start()
	// Both tasks start together, then feed their own samples: "/slow" moves
	// 100 B/s and "/fast" 200 B/s, so the lines must not share a rate.
	r.OnEvent(transfer.Event{Path: "/slow.bin", Kind: transfer.EventTaskStart})
	r.OnEvent(transfer.Event{Path: "/fast.bin", Kind: transfer.EventTaskStart})

	r.now = func() time.Time { return base.Add(1 * time.Second) }
	r.OnEvent(transfer.Event{Path: "/slow.bin", Current: 100, Total: 2000, Kind: transfer.EventProgress})
	r.OnEvent(transfer.Event{Path: "/fast.bin", Current: 200, Total: 1000, Kind: transfer.EventProgress})
	r.now = func() time.Time { return base.Add(2 * time.Second) }
	r.OnEvent(transfer.Event{Path: "/slow.bin", Current: 200, Total: 2000, Kind: transfer.EventProgress})
	r.OnEvent(transfer.Event{Path: "/fast.bin", Current: 400, Total: 1000, Kind: transfer.EventProgress})

	out.Reset()
	r.paint()
	frame := out.String()
	r.Stop()

	// 1800 B left at 100 B/s and 600 B left at 200 B/s: 18s and 3s, each
	// derived from the task's own slope and printed next to that slope.
	for _, want := range []string{
		"0.10KiB/s ETA: 18s",
		"0.20KiB/s ETA: 3s",
		"Total: 0.29KiB/s | Remaining: 2 ETA: 21s",
	} {
		if !strings.Contains(frame, want) {
			t.Errorf("frame missing %q: %q", want, frame)
		}
	}
}

// TestRendererTaskRateFallsBackToAverage locks the review-approved Δ: a task
// whose window holds fewer than two live samples reports its session average
// instead of zero, so the ETA survives chunk-grained progress (upstream's
// rate_avg branch, downloader.cpp:3493-3496).
func TestRendererTaskRateFallsBackToAverage(t *testing.T) {
	var out bytes.Buffer
	r := newRenderer(&out, false, false, 0, time.Hour, func() int { return 200 })
	base := time.Unix(1_000_000, 0)

	r.now = func() time.Time { return base }
	r.Start()
	r.OnEvent(transfer.Event{Path: "/a.bin", Kind: transfer.EventTaskStart})

	// Ten seconds in, one sample only: 100 B over 10s is 10 B/s, so 200 B
	// left is a 20s ETA rather than no ETA at all.
	r.now = func() time.Time { return base.Add(10 * time.Second) }
	r.OnEvent(transfer.Event{Path: "/a.bin", Current: 100, Total: 300, Kind: transfer.EventProgress})

	out.Reset()
	r.paint()
	frame := out.String()
	if !strings.Contains(frame, "0.01KiB/s ETA: 20s") {
		t.Errorf("frame = %q, want the average rate and its 20s ETA", frame)
	}

	// Another 50 seconds with no sample at all: the window is empty, so the
	// task still falls back to its average (now 100 B over 60s). The ETA
	// survives — it must not vanish while the task is still running.
	r.now = func() time.Time { return base.Add(60 * time.Second) }
	if got := r.tasks["/a.bin"].rate(r.now()); got <= 0 {
		t.Errorf("rate after the window aged out = %v, want the task average", got)
	}
	out.Reset()
	r.paint()
	frame = out.String()
	r.Stop()
	if !strings.Contains(frame, "ETA: ") {
		t.Errorf("frame = %q, want an ETA from the task average", frame)
	}
}
