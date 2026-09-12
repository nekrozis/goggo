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
	r := newRenderer(&out, false, false, 0, time.Hour, func() int { return 120 }, nil, 2)
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

// fakeProgress is a progressSource a test drives by hand: the renderer polls
// it, exactly as it polls the run's transfer.Progress.
type fakeProgress struct {
	bytes map[string]int64
	total map[string]int64

	queueTasks     int
	queueBytes     int64
	queuePublished bool
}

func newFakeProgress() *fakeProgress {
	return &fakeProgress{bytes: map[string]int64{}, total: map[string]int64{}}
}

func (f *fakeProgress) set(task string, value, total int64) {
	f.bytes[task] = value
	f.total[task] = total
}

// setQueue publishes the run-level snapshot, the way transfer.Run does.
func (f *fakeProgress) setQueue(tasks int, bytes int64) {
	f.queueTasks, f.queueBytes, f.queuePublished = tasks, bytes, true
}

func (f *fakeProgress) Queue() (int, int64, bool) {
	if !f.queuePublished {
		return 0, 0, false
	}
	return f.queueTasks, f.queueBytes, true
}

func (f *fakeProgress) Bytes(task string) (int64, bool) {
	v, ok := f.bytes[task]
	return v, ok
}

func (f *fakeProgress) Total(task string) (int64, bool) {
	v, ok := f.total[task]
	return v, ok
}

// TestRendererSamplesFromSource locks the S-ETA2 wiring: the repaint loop polls
// the source for the sample and the total, so a task whose first chunk is still
// arriving already has a rate and an ETA. The displayed bytes keep coming from
// the progress events, which is the split the review approved.
func TestRendererSamplesFromSource(t *testing.T) {
	var out bytes.Buffer
	source := newFakeProgress()
	r := newRenderer(&out, false, false, 0, time.Hour, func() int { return 200 }, source, 2)
	base := time.Unix(1_000_000, 0)

	r.now = func() time.Time { return base }
	r.Start()
	r.OnEvent(transfer.Event{Path: "/a.bin", Kind: transfer.EventTaskStart})

	// No progress event at all: only the sampler moves. Two ticks inside the
	// window give it a slope, and the total the ETA needs comes from the same
	// surface — the events carry a total only once a chunk has landed.
	r.now = func() time.Time { return base.Add(1 * time.Second) }
	source.set("/a.bin", 100, 1000)
	r.paint()
	r.now = func() time.Time { return base.Add(2 * time.Second) }
	source.set("/a.bin", 200, 1000)
	out.Reset()
	r.paint()
	frame := out.String()
	r.Stop()

	if !strings.Contains(frame, "0.10KiB/s ETA: 10s") {
		t.Errorf("frame = %q, want the sampled rate and its 10s ETA", frame)
	}
	// The display still follows the events: none has arrived, so the bytes
	// and the percentage show zero (review S-ETA2).
	if !strings.Contains(frame, "0.00 B/1000.00 B") || !strings.Contains(frame, "  0% ") {
		t.Errorf("frame = %q, want event-driven displayed bytes", frame)
	}
}

// TestRendererQueueRemainingAndTotalETA locks the S-ETA3 total line: the
// pending side is the run's queue snapshot minus the tasks that have started,
// its size is the pending bytes rather than the active remainder, and the rate
// prefix follows the configured thread count.
func TestRendererQueueRemainingAndTotalETA(t *testing.T) {
	var out bytes.Buffer
	source := newFakeProgress()
	source.setQueue(4, 4000)
	r := newRenderer(&out, false, false, 0, time.Hour, func() int { return 200 }, source, 4)
	base := time.Unix(1_000_000, 0)

	r.now = func() time.Time { return base }
	r.Start()
	r.OnEvent(transfer.Event{Path: "/a.bin", Kind: transfer.EventTaskStart})
	r.OnEvent(transfer.Event{Path: "/b.bin", Kind: transfer.EventTaskStart})
	source.set("/a.bin", 100, 1000)
	source.set("/b.bin", 100, 1000)
	r.now = func() time.Time { return base.Add(1 * time.Second) }
	r.paint()
	r.now = func() time.Time { return base.Add(2 * time.Second) }
	source.set("/a.bin", 200, 1000)
	source.set("/b.bin", 200, 1000)
	out.Reset()
	r.paint()
	frame := out.String()
	r.Stop()

	// Two of the four tasks started (2000 of the 4000 queued bytes), each
	// running at 100 B/s: 10s to finish the 2000 pending bytes at the 200 B/s
	// aggregate rate, plus 10s of each running task's own ETA.
	if !strings.Contains(frame, "Total: 0.20KiB/s | Remaining: 2 (1.95 KiB) ETA: 30s") {
		t.Errorf("frame = %q, want the queue-derived total line", frame)
	}
}

// TestRendererTotalPrefixFollowsThreads locks Δ-ETA1-3: the rate prefix on the
// total line follows the configured thread count, the way upstream keys it on
// iThreads, not on how many tasks this renderer happens to have seen.
func TestRendererTotalPrefixFollowsThreads(t *testing.T) {
	for _, tc := range []struct {
		name    string
		threads uint32
		want    bool
	}{
		{name: "single thread", threads: 1, want: false},
		{name: "several threads", threads: 4, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			r := newRenderer(&out, false, false, 0, time.Hour, func() int { return 200 }, nil, tc.threads)
			r.now = func() time.Time { return time.Unix(1_000_000, 0) }
			r.OnEvent(transfer.Event{Path: "/a.bin", Kind: transfer.EventTaskStart})
			r.OnEvent(transfer.Event{Path: "/b.bin", Kind: transfer.EventTaskStart})
			r.paint()
			frame := out.String()

			if got := strings.Contains(frame, "Total: "); got != tc.want {
				t.Errorf("frame = %q, want the Total prefix to be %v", frame, tc.want)
			}
			if !strings.Contains(frame, "Remaining: 2") {
				t.Errorf("frame = %q, want the remaining count", frame)
			}
		})
	}
}

// TestRendererPendingUnderflowClamps locks the review's underflow rule: a queue
// snapshot smaller than what the started tasks carry is a state disagreement,
// so the pending numbers clamp at zero instead of feeding a wrapped value into
// the display (review S-ETA3).
func TestRendererPendingUnderflowClamps(t *testing.T) {
	var out bytes.Buffer
	source := newFakeProgress()
	source.setQueue(1, 100) // less than the two started tasks already carry
	r := newRenderer(&out, false, false, 0, time.Hour, func() int { return 200 }, source, 4)
	base := time.Unix(1_000_000, 0)

	r.now = func() time.Time { return base }
	r.Start()
	r.OnEvent(transfer.Event{Path: "/a.bin", Kind: transfer.EventTaskStart})
	r.OnEvent(transfer.Event{Path: "/b.bin", Kind: transfer.EventTaskStart})
	source.set("/a.bin", 100, 1000)
	source.set("/b.bin", 100, 1000)
	r.now = func() time.Time { return base.Add(1 * time.Second) }
	r.paint()
	out.Reset()
	r.paint()
	frame := out.String()
	r.Stop()

	if !strings.Contains(frame, "Remaining: 0") {
		t.Errorf("frame = %q, want the clamped remaining count", frame)
	}
	if strings.Contains(frame, "(") {
		t.Errorf("frame = %q, want no pending size group", frame)
	}
}

// TestRateWindowResetsOnADecrease locks the reviewed answer to a backwards
// sample: drop the old window and start again from the new value, never a
// negative slope and never a clamp.
func TestRateWindowResetsOnADecrease(t *testing.T) {
	var w rateWindow
	w.cap = 100
	t0 := time.Unix(1_000_000, 0)

	w.addSample(t0, 100)
	w.addSample(t0.Add(1*time.Second), 200)
	w.addSample(t0.Add(2*time.Second), 300)
	if got := w.rate(t0.Add(2 * time.Second)); got != 100 {
		t.Fatalf("rate = %v, want the 100 B/s slope", got)
	}

	// A hash mismatch emptied the chunk buffer: the task went backwards.
	w.addSample(t0.Add(3*time.Second), 50)
	if len(w.points) != 1 || w.points[0][1] != 50 {
		t.Fatalf("window = %v, want the single post-reset sample", w.points)
	}
	if got := w.rate(t0.Add(3 * time.Second)); got != 0 {
		t.Errorf("rate right after the reset = %v, want 0", got)
	}

	w.addSample(t0.Add(4*time.Second), 150)
	if got := w.rate(t0.Add(4 * time.Second)); got != 100 {
		t.Errorf("rate after the reset = %v, want the post-reset slope (100 B/s)", got)
	}
	w.reset()
	if len(w.points) != 0 {
		t.Errorf("points after reset = %d, want none", len(w.points))
	}
}

// TestRendererFoldAndWindowBehavior is not needed here: the fold lives in
// transfer; this file keeps the renderer's display semantics only. A task's
// first frame has no elapsed time behind its average, so no rate and no ETA
// print yet.
func TestRendererFirstFrameHasNoETA(t *testing.T) {
	var out bytes.Buffer
	r := newRenderer(&out, false, false, 0, time.Hour, func() int { return 120 }, nil, 2)
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
	r := newRenderer(&out, false, false, 0, time.Hour, func() int { return 200 }, nil, 2)
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
	r := newRenderer(&out, false, false, 0, time.Hour, func() int { return 200 }, nil, 2)
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
