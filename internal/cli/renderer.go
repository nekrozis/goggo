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

// renderer consumes the full transfer event stream the way the C++
// printProgress loop renders it (downloader.cpp:3505-3630): messages print as
// they arrive, progress updates fold into a per-task view, and a redraw loop
// repaints the task lines and the total line once per progress interval.
//
// It implements transfer.Observer structurally; core hands the whole stream to
// it through the optional-ability assertion (review D75) and keeps its own
// message-only path for plain consoles. Every rate is the task's own: the
// samples a task's progress events feed go into that task's sliding window,
// the way upstream keeps one TimeAndSize deque per download thread
// (downloader.cpp:3483). A task's ETA and its displayed rate therefore share
// one number instead of leaning on the other tasks' traffic (review S-ETA1).
type renderer struct {
	out      io.Writer
	bar      *progress.Bar
	interval time.Duration
	width    func() int
	unit     uint32
	now      func() time.Time // swappable for tests

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
// the terminal width for line trimming; nil means a fixed 80 columns.
func newRenderer(out io.Writer, useUnicode, useColor bool, unit uint32, interval time.Duration, width func() int) *renderer {
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
		now:      time.Now,
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
		// The sample is this task's own cumulative progress, the equivalent
		// of upstream pushing that thread's dlnow into its deque
		// (downloader.cpp:3483).
		t.window.add(r.now(), t.done)
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

	if finished < len(r.order) {
		remaining := len(r.order) - finished
		totalLine := ""
		// The total rate is the sum of the running tasks' rates, the way
		// upstream's total_rate accumulates its per-thread rates
		// (downloader.cpp:3562, 3614). Finished tasks dropped out above, as
		// they do upstream.
		if len(r.order) > 1 {
			totalLine += "Total: " + util.RateString(totalRate, r.unit) + " | "
		}
		totalLine += fmt.Sprintf("Remaining: %d", remaining)
		if totalETASecs > 0 {
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
