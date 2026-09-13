package cli

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/nekrozis/goggo/internal/transfer"
	"github.com/nekrozis/goggo/internal/ui/progress"
)

// fakeSink records everything a renderer hands over, so the view-model tests
// can assert on the derived state instead of scraping formatted strings.
type fakeSink struct {
	frames   [][]string
	infos    []string
	diags    []string
	starts   []string
	finishes []string
	ticks    []viewModel
	reasons  []stopReason
	stats    []runStats
}

func (s *fakeSink) info(text string)             { s.infos = append(s.infos, text) }
func (s *fakeSink) diagnostic(text string)       { s.diags = append(s.diags, text) }
func (s *fakeSink) taskStart(_ int, path string) { s.starts = append(s.starts, path) }
func (s *fakeSink) taskFinish(_ int, path string) {
	s.finishes = append(s.finishes, path)
}
func (s *fakeSink) tick(vm viewModel) { s.ticks = append(s.ticks, vm) }
func (s *fakeSink) finalize(r stopReason, st runStats) {
	s.reasons = append(s.reasons, r)
	s.stats = append(s.stats, st)
}

var _ sink = (*fakeSink)(nil)

// fakeProgress is a progressSource a test drives by hand: the renderer polls
// it, exactly as it polls the run's transfer.Progress.
type fakeProgress struct {
	bytes  map[string]int64
	total  map[string]int64
	tasks  int
	qbytes int64
}

func newFakeProgress() *fakeProgress {
	return &fakeProgress{bytes: map[string]int64{}, total: map[string]int64{}}
}

func (f *fakeProgress) set(task string, value, total int64) {
	f.bytes[task] = value
	f.total[task] = total
}

func (f *fakeProgress) setQueue(tasks int, bytes int64) { f.tasks, f.qbytes = tasks, bytes }
func (f *fakeProgress) Queue() (int, int64, bool)       { return f.tasks, f.qbytes, true }
func (f *fakeProgress) Bytes(task string) (int64, bool) { v, ok := f.bytes[task]; return v, ok }
func (f *fakeProgress) Total(task string) (int64, bool) { v, ok := f.total[task]; return v, ok }

var _ progressSource = (*fakeProgress)(nil)

// newTestRenderer wires a renderer over a fake sink with a swappable clock.
// The interval is an hour, so the goroutine ticker never fires inside a test:
// ticks happen when the test calls r.tick().
func newTestRenderer(src progressSource, now *time.Time) (*renderer, *fakeSink) {
	s := &fakeSink{}
	r := newRenderer(s, progress.NewBar(false, false), time.Hour, src)
	r.now = func() time.Time { return *now }
	return r, s
}

// TestViewModelReadsProgressOnly locks the single numeric authority (review
// UI1 v3 §6.A): the view model's done/total come from Progress.Bytes/Total,
// and a progress event's Current is never consumed — a lying event must not
// move the display.
func TestViewModelReadsProgressOnly(t *testing.T) {
	src := newFakeProgress()
	src.set("/a.bin", 600, 1000)
	now := time.Unix(1_000_000, 0)
	r, s := newTestRenderer(src, &now)

	r.OnEvent(transfer.Event{Path: "/a.bin", Kind: transfer.EventTaskStart})
	// The event claims 100 bytes; Progress says 600. Progress wins.
	r.OnEvent(transfer.Event{Path: "/a.bin", Current: 100, Total: 1000, Kind: transfer.EventProgress})

	vm := r.buildVM(now)
	if len(vm.tasks) != 1 {
		t.Fatalf("tasks = %d, want 1", len(vm.tasks))
	}
	if vm.tasks[0].done != 600 || vm.tasks[0].total != 1000 {
		t.Errorf("done/total = %d/%d, want 600/1000 from Progress", vm.tasks[0].done, vm.tasks[0].total)
	}
	if vm.active != 1 {
		t.Errorf("active = %d, want 1", vm.active)
	}
	_ = s
}

// TestViewModelClamps locks the display-layer defensive clamps (review UI1 v3
// §5): a negative sample reads as zero, a sample past the total reads as the
// total, and a pending side that would go negative clamps at zero.
func TestViewModelClamps(t *testing.T) {
	src := newFakeProgress()
	src.set("/neg.bin", -5, 100)
	src.set("/past.bin", 150, 100)
	now := time.Unix(1_000_000, 0)
	r, _ := newTestRenderer(src, &now)
	r.OnEvent(transfer.Event{Path: "/neg.bin", Kind: transfer.EventTaskStart})
	r.OnEvent(transfer.Event{Path: "/past.bin", Kind: transfer.EventTaskStart})

	vm := r.buildVM(now)
	if vm.tasks[0].done != 0 {
		t.Errorf("negative sample = %d, want 0", vm.tasks[0].done)
	}
	if vm.tasks[1].done != 100 {
		t.Errorf("overshooting sample = %d, want the total 100", vm.tasks[1].done)
	}

	// The snapshot lies low: the pending side clamps at zero instead of
	// feeding a negative into the remaining bytes.
	src.setQueue(1, 50)
	vm = r.buildVM(now)
	if vm.queued != 0 || vm.remaining != 100 {
		t.Errorf("queued/remaining = %d/%d, want 0/100 (startedBytes 200 > snapshot 50)", vm.queued, vm.remaining)
	}
}

// TestETAPriority locks the boundary order (review UI1 v3 §5, constraint 3):
// remaining==0 wins over rate==0, so a finished run shows 0s rather than
// losing the ETA to the zero-rate omission.
func TestETAPriority(t *testing.T) {
	src := newFakeProgress()
	src.set("/a.bin", 1000, 1000)
	src.setQueue(1, 1000)
	now := time.Unix(1_000_000, 0)
	r, _ := newTestRenderer(src, &now)
	r.OnEvent(transfer.Event{Path: "/a.bin", Kind: transfer.EventTaskStart})

	// Everything done, no rate at all: remaining==0 ⇒ ETA 0, valid.
	vm := r.buildVM(now)
	if vm.remaining != 0 || !vm.etaValid || vm.etaSecs != 0 {
		t.Errorf("remaining/eta = %d/%v/%v, want 0/valid/0", vm.remaining, vm.etaValid, vm.etaSecs)
	}

	// Something left but no rate anywhere: the ETA group is omitted.
	src.set("/b.bin", 0, 500)
	src.setQueue(2, 1500)
	r.OnEvent(transfer.Event{Path: "/b.bin", Kind: transfer.EventTaskStart})
	vm = r.buildVM(now)
	if vm.remaining != 500 {
		t.Fatalf("remaining = %d, want 500", vm.remaining)
	}
	if vm.etaValid {
		t.Errorf("eta = %v valid at rate 0, want omitted", vm.etaSecs)
	}
}

// TestTaskFinishLeavesActiveModel locks the lifecycle split (review UI1 v3
// §6.A): a finished task leaves the active model, the finished count grows,
// the row numbering stays stable and the pending side keeps the finished
// task's bytes because they were accumulated at TaskStart.
func TestTaskFinishLeavesActiveModel(t *testing.T) {
	src := newFakeProgress()
	src.set("/a.bin", 1000, 1000)
	src.set("/b.bin", 0, 500)
	src.setQueue(3, 3000) // 3000 snapshot − 1500 started = 1500 pending
	now := time.Unix(1_000_000, 0)
	r, s := newTestRenderer(src, &now)

	r.OnEvent(transfer.Event{Path: "/a.bin", Kind: transfer.EventTaskStart})
	r.OnEvent(transfer.Event{Path: "/b.bin", Kind: transfer.EventTaskStart})
	r.OnEvent(transfer.Event{Path: "/a.bin", Kind: transfer.EventTaskFinish})

	vm := r.buildVM(now)
	if vm.active != 1 {
		t.Errorf("active = %d, want 1 after the finish", vm.active)
	}
	if r.finishedCount != 1 {
		t.Errorf("finishedCount = %d, want 1", r.finishedCount)
	}
	// /b is still row #2 — the numbering does not shift.
	if len(vm.tasks) != 1 || vm.tasks[0].index != 2 || vm.tasks[0].path != "/b.bin" {
		t.Errorf("tasks = %+v, want only /b.bin at index 2", vm.tasks)
	}
	// pendingBytes = 3000 − (1000+500) = 1500; activeRemaining = 500.
	if vm.remaining != 2000 {
		t.Errorf("remaining = %d, want 2000 (1500 pending + 500 active)", vm.remaining)
	}
	if len(s.finishes) != 1 || s.finishes[0] != "/a.bin" {
		t.Errorf("finish paths = %v, want [/a.bin] (the task that finished)", s.finishes)
	}
}

// TestPerTaskRatesRemain locks the migrated S-ETA1 semantics: each task's rate
// comes from its own window, and the aggregate is the sum of the running
// tasks' rates.
func TestPerTaskRatesRemain(t *testing.T) {
	src := newFakeProgress()
	src.set("/slow.bin", 100, 2000)
	src.set("/fast.bin", 200, 1000)
	base := time.Unix(1_000_000, 0)
	now := base
	r, _ := newTestRenderer(src, &now)
	r.OnEvent(transfer.Event{Path: "/slow.bin", Kind: transfer.EventTaskStart})
	r.OnEvent(transfer.Event{Path: "/fast.bin", Kind: transfer.EventTaskStart})

	now = base.Add(1 * time.Second)
	src.set("/slow.bin", 200, 2000)
	src.set("/fast.bin", 400, 1000)
	r.buildVM(now) // the 1s repaint samples the window at that instant
	now = base.Add(2 * time.Second)
	src.set("/slow.bin", 300, 2000)
	src.set("/fast.bin", 600, 1000)

	vm := r.buildVM(now)
	if got := vm.tasks[0].rate; got != 100 {
		t.Errorf("slow rate = %v, want 100 B/s", got)
	}
	if got := vm.tasks[1].rate; got != 200 {
		t.Errorf("fast rate = %v, want 200 B/s", got)
	}
	if got := vm.rate; got != 300 {
		t.Errorf("aggregate rate = %v, want 300 B/s", got)
	}
}

// TestTaskRateFallsBackToAverage locks the migrated S-ETA1 delta: with fewer
// than two live samples the task reports its session average, and the average
// survives a stalled window.
func TestTaskRateFallsBackToAverage(t *testing.T) {
	src := newFakeProgress()
	src.set("/a.bin", 100, 300)
	base := time.Unix(1_000_000, 0)
	now := base
	r, _ := newTestRenderer(src, &now)
	r.OnEvent(transfer.Event{Path: "/a.bin", Kind: transfer.EventTaskStart})

	// Ten seconds in, one sample only: 100 B over 10 s is 10 B/s.
	now = base.Add(10 * time.Second)
	src.set("/a.bin", 100, 300)
	vm := r.buildVM(now)
	if got := vm.tasks[0].rate; got != 10 {
		t.Errorf("rate = %v, want the 10 B/s session average", got)
	}

	// Sixty seconds with no new sample: the window aged out, the average is
	// still there (100 B over 60 s).
	now = base.Add(60 * time.Second)
	vm = r.buildVM(now)
	if got := vm.tasks[0].rate; got <= 0 {
		t.Errorf("rate after the window aged out = %v, want the task average", got)
	}
}

// TestStopFinalizesOnce locks the Stop lifecycle (review UI1 v3 §6.E): the
// first Stop emits the terminal state exactly once and ends the repaint loop;
// further Stops are no-ops.
func TestStopFinalizesOnce(t *testing.T) {
	src := newFakeProgress()
	src.set("/a.bin", 0, 100)
	now := time.Unix(1_000_000, 0)
	r, s := newTestRenderer(src, &now)
	r.OnEvent(transfer.Event{Path: "/a.bin", Kind: transfer.EventTaskStart})
	r.OnEvent(transfer.Event{Path: "/a.bin", Kind: transfer.EventTaskFinish})

	r.Stop(stopCompleted)
	r.Stop(stopCompleted) // no-op

	if len(s.reasons) != 1 || s.reasons[0] != stopCompleted || s.stats[0].completed != 1 {
		t.Errorf("finalize calls = %v/%v, want one (completed, 1)", s.reasons, s.stats)
	}
	// The repaint loop is over: ticks after Stop change nothing.
	before := len(s.ticks)
	r.tick()
	if len(s.ticks) != before {
		t.Error("tick after Stop produced output")
	}
}

// TestFinalLines locks the terminal states (review UI1 v3 §6.E, UI1-R2 §5
// scene ②): only the completed state carries the completion count — a
// canceled run's active tasks were interrupted, not finished — and a
// zero-transfer completed run says nothing (the plan's "Nothing to download."
// already spoke for it).
func TestFinalLines(t *testing.T) {
	if got := finalLines(stopCompleted, runStats{completed: 3}); len(got) != 1 || !strings.Contains(got[0], "3") {
		t.Errorf("completed = %q, want the count", got)
	}
	// The resume aggregate line rides the terminal state (UI1-R2: markers
	// arrive mid-run, so the count lands beside the completion line).
	got := finalLines(stopCompleted, runStats{completed: 2, resumed: 2})
	if len(got) != 2 || !strings.Contains(got[0], "Resuming: 2") {
		t.Errorf("completed+resumed = %q, want the resume line before the count", got)
	}
	if got := finalLines(stopCompleted, runStats{}); got != nil {
		t.Errorf("zero-transfer completed = %q, want no final line", got)
	}
	cancel := finalLines(stopCanceled, runStats{completed: 3})
	if len(cancel) != 1 || strings.Contains(cancel[0], "3") || !strings.Contains(cancel[0], "resume") {
		t.Errorf("canceled = %q, want the resume guidance without a count", cancel)
	}
	if got := finalLines(stopFailed, runStats{completed: 3}); len(got) != 1 {
		t.Errorf("failed = %q, want one state line", got)
	}
}

// TestExitCodeAuthority locks the single exit-code authority (review UI1 v3
// constraint 8, extended by CLI1 §8): every outcome maps to one number, and the
// install lifecycle's reasons are one of the paths into it — 0/1/2/130 and
// nothing else.
func TestExitCodeAuthority(t *testing.T) {
	for _, tc := range []struct {
		outcome outcome
		want    int
	}{
		{outcomeOK, 0},
		{outcomeOperationFailure, 1},
		{outcomeUsageFailure, 2},
		{outcomeInterrupted, 130},
	} {
		if got := exitCode(tc.outcome); got != tc.want {
			t.Errorf("exitCode(%d) = %d, want %d", tc.outcome, got, tc.want)
		}
	}
	// The install lifecycle reaches the same authority through stopOutcome.
	for reason, want := range map[stopReason]outcome{
		stopCompleted: outcomeOK,
		stopCanceled:  outcomeInterrupted,
		stopFailed:    outcomeOperationFailure,
	} {
		if got := stopOutcome(reason); got != want {
			t.Errorf("stopOutcome(%d) = %d, want %d", reason, got, want)
		}
	}
	// A parser refusal is a usage failure, everything else an operational one.
	if outcomeForError(usagef("bad")) != outcomeUsageFailure {
		t.Error("usage errors must map to the usage exit code")
	}
	if outcomeForError(errors.New("boom")) != outcomeOperationFailure {
		t.Error("plain errors must map to the operational failure code")
	}
}

// TestClassifyInstallResult locks the outcome classification: cancellation is
// recognized from the error chain or the live context, everything else that
// failed is failed.
func TestClassifyInstallResult(t *testing.T) {
	if got := classifyInstallResult(nil, context.Background()); got != stopCompleted {
		t.Errorf("nil error = %d, want completed", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := classifyInstallResult(ctx.Err(), ctx); got != stopCanceled {
		t.Errorf("context.Canceled = %d, want canceled", got)
	}
	wrapped := fmtWrapped{context.Canceled}
	if got := classifyInstallResult(wrapped, context.Background()); got != stopCanceled {
		t.Errorf("wrapped cancel = %d, want canceled", got)
	}
	if got := classifyInstallResult(errors.New("boom"), context.Background()); got != stopFailed {
		t.Errorf("plain error = %d, want failed", got)
	}
}

type fmtWrapped struct{ err error }

func (w fmtWrapped) Error() string { return "install: " + w.err.Error() }
func (w fmtWrapped) Unwrap() error { return w.err }

// TestLogSinkNeverEmitsProgress locks the non-TTY contract (review UI1 v3
// §6.C): lifecycle and message events become lines, progress events never do.
func TestLogSinkNeverEmitsProgress(t *testing.T) {
	var out, errOut strings.Builder
	s := &logSink{out: &out, errOut: &errOut, unit: 0, now: time.Now, lastSummary: time.Now()}
	src := newFakeProgress()
	src.set("/a.bin", 0, 100)
	now := time.Unix(1_000_000, 0)
	r := newRenderer(s, progress.NewBar(false, false), time.Hour, src)
	r.now = func() time.Time { return now }

	r.OnEvent(transfer.Event{Path: "/a.bin", Kind: transfer.EventTaskStart})
	for i := 0; i < 50; i++ {
		r.OnEvent(transfer.Event{Path: "/a.bin", Current: int64(i), Total: 100, Kind: transfer.EventProgress})
	}
	r.OnEvent(transfer.Event{Path: "/a.bin", Kind: transfer.EventTaskFinish})

	if got := out.String(); strings.Count(got, "\n") != 2 {
		t.Errorf("stdout = %q, want exactly the start and the finish lines", got)
	}
	if strings.Contains(out.String(), "\x1b") {
		t.Error("the log sink emitted an ANSI escape")
	}
}

// TestLogSinkCadence locks the summary rhythm (review UI1 v3 §6.C): 10s since
// the last summary OR 10 finishes, whichever comes first, both reset on emit.
func TestLogSinkCadence(t *testing.T) {
	var out, errOut strings.Builder
	base := time.Unix(1_000_000, 0)
	now := base
	s := &logSink{out: &out, errOut: &errOut, unit: 0, now: func() time.Time { return now }, lastSummary: base}

	vm := viewModel{active: 1, remaining: 100, etaValid: true, etaSecs: 5}
	// Nine finishes inside the window: below both thresholds.
	for i := 0; i < 9; i++ {
		s.taskFinish(i, "x")
		s.tick(vm)
	}
	if strings.Contains(out.String(), "active ·") {
		t.Fatalf("summary emitted below both thresholds: %q", out.String())
	}
	// The tenth finish crosses the increment threshold.
	s.taskFinish(9, "x")
	s.tick(vm)
	if !strings.Contains(out.String(), "active ·") {
		t.Errorf("no summary at the 10-finish threshold: %q", out.String())
	}
	// Both thresholds reset: nine more finishes stay quiet.
	out.Reset()
	for i := 0; i < 9; i++ {
		s.taskFinish(i, "x")
		s.tick(vm)
	}
	if strings.Contains(out.String(), "active ·") {
		t.Errorf("thresholds did not reset: %q", out.String())
	}
	// The clock crossing 10s emits regardless of the finish count.
	now = base.Add(11 * time.Second)
	s.tick(vm)
	if !strings.Contains(out.String(), "active ·") {
		t.Errorf("no summary at the 10s threshold: %q", out.String())
	}
}

// TestLogSinkFinalizeStreams locks the terminal-state streams: completed
// lands on stdout, canceled and failed on stderr.
func TestLogSinkFinalizeStreams(t *testing.T) {
	var out, errOut strings.Builder
	s := &logSink{out: &out, errOut: &errOut, unit: 0, now: time.Now, lastSummary: time.Now()}
	s.finalize(stopCompleted, runStats{completed: 2})
	s.finalize(stopCanceled, runStats{completed: 2})
	if strings.Count(out.String(), "\n") != 1 || !strings.Contains(out.String(), "2") {
		t.Errorf("stdout = %q, want only the completed count", out.String())
	}
	if strings.Count(errOut.String(), "\n") != 1 || !strings.Contains(errOut.String(), "resume") {
		t.Errorf("stderr = %q, want only the interrupted line", errOut.String())
	}
}

// TestRateWindowResetsOnADecrease locks the reviewed answer to a backwards
// sample: the window resets and restarts from the new value (review S-ETA2).
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

// TestStopBeforeStartThenStart is the UI1-R1 lifecycle regression: Stop with
// no Start finalizes synchronously, a later Start must be a no-op — the
// finalize flag bars a ticker that would never see a stop signal again — and
// a second Stop stays silent. No goroutine residue, no output, no block.
func TestStopBeforeStartThenStart(t *testing.T) {
	src := newFakeProgress()
	src.set("/a.bin", 0, 100)
	now := time.Unix(1_000_000, 0)
	r, s := newTestRenderer(src, &now)

	before := runtime.NumGoroutine()
	r.Stop(stopFailed)
	r.Start()
	r.Stop(stopCanceled)

	if len(s.reasons) != 1 || s.reasons[0] != stopFailed {
		t.Errorf("finalize calls = %v, want exactly the first Stop's failed", s.reasons)
	}
	// The frame is final: ticks refuse, nothing renders, nothing blocks.
	r.tick()
	if len(s.ticks) != 0 {
		t.Errorf("ticks = %d, want none after finalize", len(s.ticks))
	}
	if after := runtime.NumGoroutine(); after > before {
		t.Errorf("goroutines before/after = %d/%d, want no ticker residue", before, after)
	}
}

// TestDisplayPathFollowsInstallRoot locks the UI1-R2 seam: task rows are
// relative to the plan's semantic install root once core hands it over, and
// keep the absolute form before that (never wrong, only verbose). The
// install root is the resolved %install_dir% — not a guessed prefix.
func TestDisplayPathFollowsInstallRoot(t *testing.T) {
	src := newFakeProgress()
	now := time.Unix(1_000_000, 0)
	r, s := newTestRenderer(src, &now)
	root := "C:/Games/HoMM 3 Complete"

	r.OnEvent(transfer.Event{Path: root + "/Data/VIDEO.VID", Kind: transfer.EventTaskStart})
	if len(s.starts) != 1 || s.starts[0] != root+"/Data/VIDEO.VID" {
		t.Fatalf("starts before the root is known = %v, want the absolute path", s.starts)
	}
	r.SetInstallRoot(root)
	r.OnEvent(transfer.Event{Path: root + "/EULA/EULA US.doc", Kind: transfer.EventTaskStart})
	if s.starts[1] != "EULA/EULA US.doc" {
		t.Errorf("starts after the root = %v, want the relative display path", s.starts)
	}
	// The active frame rows carry display paths too.
	vm := r.buildVM(now)
	if vm.tasks[0].path != "Data/VIDEO.VID" {
		t.Errorf("task row path = %q, want relative", vm.tasks[0].path)
	}
	// A path outside the root cannot be made relative; it passes through.
	r.OnEvent(transfer.Event{Path: "C:/Elsewhere/x.bin", Kind: transfer.EventTaskStart})
	if s.starts[2] != "C:/Elsewhere/x.bin" {
		t.Errorf("outside-root path = %v, want it unchanged", s.starts[2])
	}
}

// TestMarkersAggregate locks decision 1's UI side: the resume/skip markers
// are explicit signals counted by the renderer, never display material. The
// resume count rides the summary; neither marker produces an info line.
func TestMarkersAggregate(t *testing.T) {
	src := newFakeProgress()
	now := time.Unix(1_000_000, 0)
	r, s := newTestRenderer(src, &now)

	for _, path := range []string{"/a.bin", "/b.bin"} {
		r.OnEvent(transfer.Event{Path: path, Kind: transfer.EventTaskStart})
		r.OnEvent(transfer.Event{Path: path, Text: transfer.ResumeMessage(3, path), Kind: transfer.EventMessageInfo})
		r.OnEvent(transfer.Event{Path: path, Kind: transfer.EventTaskFinish})
	}
	r.OnEvent(transfer.Event{Path: "/c.bin", Kind: transfer.EventTaskStart})
	r.OnEvent(transfer.Event{Path: "/c.bin", Text: transfer.SkipMessage("/c.bin"), Kind: transfer.EventMessageSuccess})
	r.OnEvent(transfer.Event{Path: "/c.bin", Kind: transfer.EventTaskFinish})

	if len(s.infos) != 0 {
		t.Errorf("info lines = %v, want none (markers aggregate)", s.infos)
	}
	r.Stop(stopCompleted)
	if s.stats[0].resumed != 2 || s.stats[0].skipped != 1 || s.stats[0].completed != 3 {
		t.Errorf("stats = %+v, want 2 resumed / 1 skipped / 3 completed", s.stats[0])
	}
}

// TestZeroTransferEmitsNothing locks scene ②: a run whose queue was empty has
// no final line — the plan's "Already up to date / Nothing to download."
// already spoke for it (review UI1-R2 §5).
func TestZeroTransferEmitsNothing(t *testing.T) {
	src := newFakeProgress()
	src.setQueue(0, 0)
	now := time.Unix(1_000_000, 0)
	r, s := newTestRenderer(src, &now)
	r.Start()
	r.Stop(stopCompleted)

	if len(s.reasons) != 1 {
		t.Fatalf("finalize calls = %d, want the sink told once", len(s.reasons))
	}
	// The stats carry the empty truth; the sink's finalLines suppress the
	// count line for a zero completed run.
	if s.stats[0].completed != 0 {
		t.Errorf("completed = %d, want 0", s.stats[0].completed)
	}
	if got := finalLines(stopCompleted, s.stats[0]); got != nil {
		t.Errorf("finalLines = %q, want none for a zero-transfer run", got)
	}
}
