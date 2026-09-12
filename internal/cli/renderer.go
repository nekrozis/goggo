package cli

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/nekrozis/goggo/internal/transfer"
	"github.com/nekrozis/goggo/internal/ui/progress"
	"github.com/nekrozis/goggo/internal/util"
)

// progressSource is the sampling surface the renderer polls, declared here
// because this is where it is consumed: transfer's *Progress satisfies it
// structurally, so the front end never depends on a transfer interface. Bytes
// is the task's logical download progress — false means "no sampling state",
// never "zero bytes" — and Total is the same task total the progress events
// carry, which is what lets a task show an ETA while its first chunk is still
// arriving. Queue is the run-level snapshot the pending count and the pending
// bytes are derived from; its false means "no snapshot", never "empty queue"
// (reviews S-ETA2, S-ETA3).
type progressSource interface {
	Bytes(task string) (int64, bool)
	Total(task string) (int64, bool)
	Queue() (tasks int, bytes int64, ok bool)
}

var _ progressSource = (*transfer.Progress)(nil)

// renderer consumes the full transfer event stream the way the C++
// printProgress loop renders it (downloader.cpp:3505-3630): messages print as
// they arrive, progress updates fold into a per-task view, and a redraw loop
// repaints the task lines and the total line once per progress interval.
//
// It implements transfer.Observer structurally; core hands the whole stream to
// it through the optional-ability assertion (review D75) and keeps its own
// message-only path for plain consoles. Every rate is the task's own: the
// samples go into that task's sliding window, the way upstream keeps one
// TimeAndSize deque per download thread (downloader.cpp:3483). A task's ETA
// and its displayed rate therefore share one number instead of leaning on the
// other tasks' traffic (review S-ETA1).
//
// The samples come from two places. A source — the run's transfer.Progress —
// is polled once per repaint, which is the equivalent of upstream's progress
// callback feeding vDownloadInfo while printProgress reads it; without a
// source the progress events are the only feed, which is what the renderer's
// own tests and a plain console use (review S-ETA2).
type renderer struct {
	out      io.Writer
	bar      *progress.Bar
	interval time.Duration
	width    func() int
	unit     uint32
	threads  uint32
	now      func() time.Time // swappable for tests
	source   progressSource   // nil: sample from the progress events instead

	mu    sync.Mutex
	tasks map[string]*renderTask
	order []string
	done  map[string]bool

	stop    chan struct{}
	stopped chan struct{}
}

// renderTask is one task's view: its display path, the byte counts of the last
// progress event, and the task's own sample window and origin. The origin is
// where the task's session average counts from — the moment the renderer first
// saw the task.
type renderTask struct {
	path   string
	done   int64
	total  int64
	start  time.Time
	window rateWindow
}

// newRenderTask registers a task view, with the window defaults the review
// locked for this display (D76: 10 seconds and at most 100 samples). The
// window moved from the renderer to the task in S-ETA1, so the defaults move
// with it — a zero cap would trim every sample away.
func newRenderTask(path string, now time.Time) *renderTask {
	return &renderTask{path: path, start: now, window: rateWindow{cap: 100}}
}

// rate is the task's download rate in bytes per second: the slope of its own
// window, or its session average when the window holds fewer than two live
// samples. Upstream switches on the deque size (100 samples ≈ 10 s of 100 ms
// callbacks, downloader.cpp:3484-3496); the event model here is chunk-grained,
// so the switch is on the live samples instead (review S-ETA1, Δ). The average
// keeps the ETA of a task whose chunks are slower than the window — or of a
// task that has barely started — from vanishing. Callers hold the mutex.
func (t *renderTask) rate(now time.Time) float64 {
	if t.window.samples(now) >= 2 {
		return t.window.rate(now)
	}
	elapsed := now.Sub(t.start).Seconds()
	if elapsed <= 0 {
		return 0
	}
	return float64(t.done) / elapsed
}

// rateWindow is the sliding byte window behind the instantaneous rate: the
// 10 s, 100-point cap mirrors upstream's TimeAndSize deque
// (downloader.cpp:1541-1548).
type rateWindow struct {
	points [][2]int64 // (unixNano, cumulative installed bytes)
	cap    int
}

func (w *rateWindow) add(t time.Time, bytes int64) {
	w.points = append(w.points, [2]int64{t.UnixNano(), bytes})
	// Two trim conditions, the way the review locked the window (D76): the
	// slope only spans the last 10 seconds, and at most 100 samples are kept.
	cutoff := t.UnixNano() - int64(10*time.Second)
	keep := 0
	for keep < len(w.points) && w.points[keep][0] < cutoff {
		keep++
	}
	if keep > 0 {
		w.points = w.points[keep:]
	}
	if len(w.points) > w.cap {
		w.points = w.points[len(w.points)-w.cap:]
	}
}

// live returns the window's samples that are still inside the 10 s span at
// now — the query-time trim the review locked (D76): the samples a stall has
// aged out must not keep feeding a slope.
func (w *rateWindow) live(now time.Time) [][2]int64 {
	cutoff := now.UnixNano() - int64(10*time.Second)
	live := w.points
	for len(live) > 0 && live[0][0] < cutoff {
		live = live[1:]
	}
	return live
}

// samples counts the window's live samples at now, the number the per-task
// rate switches on.
func (w *rateWindow) samples(now time.Time) int {
	return len(w.live(now))
}

// newest returns the window's most recent sample value, whether or not it has
// aged out: it is the value a new sample is compared against.
func (w *rateWindow) newest() (int64, bool) {
	if len(w.points) == 0 {
		return 0, false
	}
	return w.points[len(w.points)-1][1], true
}

// reset drops every sample, so the window starts over from the next add.
func (w *rateWindow) reset() {
	w.points = nil
}

// addSample feeds one sampled value into the window. A value below the newest
// sample means the task went backwards — a failed hash discards the chunk
// buffer and the next attempt starts from the chunk's offset again — and the
// review locked the answer: drop the old samples and start again from this
// one. Never a negative slope, never a sample clamped back to its predecessor
// (review S-ETA2).
func (w *rateWindow) addSample(now time.Time, value int64) {
	if newest, ok := w.newest(); ok && value < newest {
		w.reset()
	}
	w.add(now, value)
}

// rate is the bytes-per-second slope over the last 10 seconds counted from
// now — the query time, not the newest sample's time. A window that holds
// fewer than two live samples has no slope to report and returns zero. Callers
// hold the renderer's mutex.
func (w *rateWindow) rate(now time.Time) float64 {
	live := w.live(now)
	if len(live) < 2 {
		return 0
	}
	first, last := live[0], live[len(live)-1]
	elapsed := float64(last[0]-first[0]) / float64(time.Second)
	if elapsed <= 0 {
		return 0
	}
	return float64(last[1]-first[1]) / elapsed
}

// newRenderer wires a renderer over the front end's streams. width supplies
// the terminal width for line trimming; nil means a fixed 80 columns. source is
// the sampling surface the repaint loop polls; nil makes the progress events
// the only sample feed. threads is the configured worker count, which decides
// whether the total line carries its rate — upstream keys that on the thread
// setting, not on the number of tasks (downloader.cpp:3612).
func newRenderer(out io.Writer, useUnicode, useColor bool, unit uint32, interval time.Duration, width func() int, source progressSource, threads uint32) *renderer {
	if width == nil {
		width = func() int { return 80 }
	}
	if interval <= 0 {
		interval = 100 * time.Millisecond
	}
	return &renderer{
		out:      out,
		bar:      progress.NewBar(useUnicode, useColor),
		interval: interval,
		width:    width,
		unit:     unit,
		threads:  threads,
		now:      time.Now,
		source:   source,
		tasks:    map[string]*renderTask{},
		done:     map[string]bool{},
		stop:     make(chan struct{}),
		stopped:  make(chan struct{}),
	}
}

// Start runs the redraw loop until Stop.
func (r *renderer) Start() {
	go func() {
		defer close(r.stopped)
		ticker := time.NewTicker(r.interval)
		defer ticker.Stop()
		for {
			select {
			case <-r.stop:
				return
			case <-ticker.C:
				r.paint()
			}
		}
	}()
}

// Stop ends the redraw loop and paints one final frame, so the lines the next
// output writes do not overlay a stale progress block.
func (r *renderer) Stop() {
	select {
	case <-r.stop:
	default:
		close(r.stop)
	}
	<-r.stopped
	r.paint()
}

// OnEvent implements transfer.Observer. The call is serial — transfer delivers
// through one goroutine (review D59) — while the redraw loop reads the same
// state, hence the mutex.
func (r *renderer) OnEvent(ev transfer.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch ev.Kind {
	case transfer.EventTaskStart:
		// TaskStart carries no byte counts (review D79): the total arrives
		// with the first progress event, and the arrival time is where this
		// task's session average starts counting.
		r.tasks[ev.Path] = newRenderTask(ev.Path, r.now())
		r.order = append(r.order, ev.Path)
	case transfer.EventProgress:
		t := r.tasks[ev.Path]
		if t == nil {
			t = newRenderTask(ev.Path, r.now())
			r.tasks[ev.Path] = t
			r.order = append(r.order, ev.Path)
		}
		t.done = ev.Current
		t.total = ev.Total
		// Without a source the events carry the only samples there are, the
		// way this display worked before the sampling surface existed. With
		// one, the repaint loop polls that instead and the events stay what
		// they are: lifecycle, not a sampling clock (review S-ETA2).
		if r.source == nil {
			t.window.add(r.now(), t.done)
		}
	case transfer.EventTaskFinish:
		r.done[ev.Path] = true
	default:
		// Message kinds print immediately, the way the C++ loop drains the
		// message queue before painting the bars.
		fmt.Fprintln(r.out, ev.Text)
	}
}

// paint repaints the whole block: the clear sequence, then one name and one
// status line per task, then the total line (downloader.cpp:3545-3615).
func (r *renderer) paint() {
	r.mu.Lock()
	defer r.mu.Unlock()

	fmt.Fprint(r.out, "\033[J\r")

	width := r.width()
	var lines []string
	var totalRate float64
	var totalETASecs float64
	finished := 0

	// The #i numbering follows the TaskStart arrival order — the review
	// locked it as the display order (D75), so r.order is used as kept. Every
	// line is measured once, at this frame's instant, so its rate, its ETA
	// and the totals all describe the same moment.
	now := r.now()
	for i, path := range r.order {
		t := r.tasks[path]
		if t == nil {
			continue
		}
		if r.done[path] {
			finished++
			lines = append(lines, fmt.Sprintf("#%d: Finished", i))
			continue
		}
		// One poll per repaint, the cadence upstream's printProgress loop
		// reads vDownloadInfo at (downloader.cpp:3523): the sample and the
		// rate it produces describe this frame. The total comes from the same
		// surface so a task whose first chunk is still arriving can show an
		// ETA — the events only carry a total once a chunk has landed
		// (review S-ETA2).
		if r.source != nil {
			if total, ok := r.source.Total(path); ok {
				t.total = total
			}
			if value, ok := r.source.Bytes(path); ok {
				t.window.addSample(now, value)
			}
		}
		fraction := 0.0
		if t.total > 0 {
			fraction = float64(t.done) / float64(t.total)
		}
		// The rate is this task's own, and the same number feeds the ETA and
		// the displayed @ rate, so "remaining / rate" is what the line shows
		// (review S-ETA1, F2/F4).
		rate := t.rate(now)
		etaSecs := 0.0
		if rate > 0 && t.total >= t.done {
			etaSecs = float64(t.total-t.done) / rate
		}
		totalRate += rate
		totalETASecs += etaSecs
		eta := ""
		if etaSecs > 0 {
			eta = " ETA: " + util.EtaString(int64(etaSecs))
		}
		pct := fmt.Sprintf("%3.0f%% ", fraction*100)
		status := fmt.Sprintf(" %s @ %s%s",
			util.SizeString(uint64(t.done), r.unit)+"/"+util.SizeString(uint64(t.total), r.unit),
			util.RateString(rate, r.unit), eta)
		barLen := 26
		if len(pct)+len(status)+barLen > width {
			barLen -= len(pct) + len(status) + barLen - width
		}
		barText := ""
		if barLen >= 5 {
			barText = r.bar.Create(barLen, fraction)
		}
		lines = append(lines,
			fmt.Sprintf("#%d %s", i, t.path),
			pct+barText+status)
	}

	// The total line describes the queue, not the active tasks: upstream keeps
	// the bytes of everything still queued and subtracts an item when a worker
	// takes it, so pending and active never overlap (downloader.cpp:3596-3620,
	// 4452). Here the pending side comes from the run's snapshot minus the
	// tasks this renderer has seen start; the active side is the ETAs above.
	// A subtract that would go negative is a state disagreement, not a number
	// worth showing, so it clamps at zero (review S-ETA3).
	started := len(r.order)
	startedBytes := int64(0)
	for _, path := range r.order {
		if t := r.tasks[path]; t != nil {
			startedBytes += t.total
		}
	}
	pending := started - finished
	pendingBytes := int64(0)
	havePending := false
	if r.source != nil {
		if tasks, bytes, ok := r.source.Queue(); ok {
			pending = max(tasks-started, 0)
			pendingBytes = max(bytes-startedBytes, 0)
			havePending = true
		}
	}

	// The line prints while anything is pending or running, so a run whose
	// first tasks have not started yet still reports its queue.
	if pending > 0 || finished < started {
		totalLine := ""
		// The total rate is the sum of the running tasks' rates, the way
		// upstream's total_rate accumulates its per-thread rates
		// (downloader.cpp:3562, 3614). Finished tasks dropped out above, as
		// they do upstream. Its prefix follows the configured thread count,
		// not the task count (downloader.cpp:3612).
		if r.threads > 1 {
			totalLine += "Total: " + util.RateString(totalRate, r.unit) + " | "
		}
		totalLine += fmt.Sprintf("Remaining: %d", pending)
		switch {
		case havePending && pendingBytes > 0 && totalRate > 0:
			// Pending bytes are spread over the aggregate rate, and the running
			// tasks add their own estimates on top (downloader.cpp:3603-3604).
			// With no rate at all there is nothing to divide by, so the group
			// is omitted rather than fed an infinity (Δ, review S-ETA3).
			eta := totalETASecs + float64(pendingBytes)/totalRate
			totalLine += fmt.Sprintf(" (%s) ETA: %s",
				util.SizeString(uint64(pendingBytes), r.unit), util.EtaString(int64(eta)))
		case !havePending && totalETASecs > 0:
			// Without a snapshot the running tasks' ETAs are all there is.
			totalLine += " ETA: " + util.EtaString(int64(totalETASecs))
		}
		lines = append(lines, totalLine)
	}

	for _, line := range lines {
		fmt.Fprintln(r.out, trimToWidth(line, width))
	}
}

// trimToWidth shortens a line to the terminal width, the way
// Util::shortenStringToTerminalWidth guards the display.
func trimToWidth(line string, width int) string {
	if width <= 0 || len(line) <= width {
		return line
	}
	return strings.TrimSpace(line[:width])
}
