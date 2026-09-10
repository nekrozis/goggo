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
// message-only path for plain consoles. The rates are the renderer's own
// estimate — one global sliding window rather than upstream's per-thread
// TimeAndSize deques (review D76, Δ).
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

	startedAt  time.Time
	startBytes int64
	window     rateWindow

	stop    chan struct{}
	stopped chan struct{}
}

// renderTask is one task's view: its display path and the byte counts of the
// last progress event.
type renderTask struct {
	path  string
	done  int64
	total int64
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

// rate is the bytes-per-second slope over the last 10 seconds counted from
// now — the query time, not the newest sample's time. A download that has
// stalled beyond the window therefore reports zero, and the ETA derived from
// it does not lean on a stale slope. Callers hold the renderer's mutex.
func (w *rateWindow) rate(now time.Time) float64 {
	cutoff := now.UnixNano() - int64(10*time.Second)
	live := w.points
	for len(live) > 0 && live[0][0] < cutoff {
		live = live[1:]
	}
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

// avgRate is the session's average rate: everything installed so far over the
// time since the run started (review D76). It is the displayed per-task rate;
// the instantaneous window rate drives the ETA and the total line.
func (r *renderer) avgRate() float64 {
	elapsed := r.now().Sub(r.startedAt).Seconds()
	if elapsed <= 0 {
		return 0
	}
	return float64(r.installedBytes()-r.startBytes) / elapsed
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
		window:   rateWindow{cap: 100},
		stop:     make(chan struct{}),
		stopped:  make(chan struct{}),
	}
}

// Start runs the redraw loop until Stop.
func (r *renderer) Start() {
	r.startedAt = r.now()
	r.startBytes = r.installedBytes()
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
		// with the first progress event.
		r.tasks[ev.Path] = &renderTask{path: ev.Path}
		r.order = append(r.order, ev.Path)
	case transfer.EventProgress:
		t := r.tasks[ev.Path]
		if t == nil {
			t = &renderTask{path: ev.Path}
			r.tasks[ev.Path] = t
			r.order = append(r.order, ev.Path)
		}
		t.done = ev.Current
		t.total = ev.Total
		r.window.add(r.now(), r.installedBytes())
	case transfer.EventTaskFinish:
		r.done[ev.Path] = true
	default:
		// Message kinds print immediately, the way the C++ loop drains the
		// message queue before painting the bars.
		fmt.Fprintln(r.out, ev.Text)
	}
}

// installedBytes sums the task views' progress.
func (r *renderer) installedBytes() int64 {
	var sum int64
	for _, t := range r.tasks {
		sum += t.done
	}
	return sum
}

// paint repaints the whole block: the clear sequence, then one name and one
// status line per task, then the total line (downloader.cpp:3545-3615).
func (r *renderer) paint() {
	r.mu.Lock()
	defer r.mu.Unlock()

	fmt.Fprint(r.out, "\033[J\r")

	width := r.width()
	var lines []string
	var totalETASecs float64
	finished := 0

	// The #i numbering follows the TaskStart arrival order — the review
	// locked it as the display order (D75), so r.order is used as kept.
	avg := r.avgRate()
	inst := r.window.rate(r.now())
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
		etaSecs := 0.0
		if inst > 0 && t.total >= t.done {
			etaSecs = float64(t.total-t.done) / inst
		}
		totalETASecs += etaSecs
		eta := ""
		if etaSecs > 0 {
			eta = " ETA: " + util.EtaString(int64(etaSecs))
		}
		pct := fmt.Sprintf("%3.0f%% ", fraction*100)
		status := fmt.Sprintf(" %s @ %s%s",
			util.SizeString(uint64(t.done), r.unit)+"/"+util.SizeString(uint64(t.total), r.unit),
			util.RateString(avg, r.unit), eta)
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
		// The total rate sums the instantaneous estimates, the way upstream's
		// total_rate accumulates per-thread rates (downloader.cpp:3572); with
		// one global window this is that window's slope (Δ1).
		if len(r.order) > 1 {
			totalLine += "Total: " + util.RateString(inst, r.unit) + " | "
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
