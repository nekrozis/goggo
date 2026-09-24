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
// structurally, so the front end never depends on a transfer interface.
//
// Queue's false means "no snapshot", never "empty queue".
type progressSource interface {
	Bytes(task string) (int64, bool)
	Total(task string) (int64, bool)
	Queue() (tasks int, bytes int64, ok bool)
}

var _ progressSource = (*transfer.Progress)(nil)

// stopReason is the terminal state an install run ended in. It is the single
// result value the exit code maps from; run.go owns that mapping.
type stopReason uint8

const (
	stopCompleted stopReason = iota
	stopCanceled
	stopFailed
)

// finalLines are the terminal's closing state for each reason. A canceled run
// never shows a completion count: its active tasks were interrupted, not
// finished. A zero-transfer run shows no count line — the plan's "Nothing to
// download." already said it.
//
// subject names what the run was, so the closing line cannot misname the command
// it closes; an empty subject keeps the install wording.
func finalLines(reason stopReason, st runStats, subject string) []string {
	switch reason {
	case stopCompleted:
		var lines []string
		if st.resumed > 0 {
			lines = append(lines, fmt.Sprintf("Resuming: %d files", st.resumed))
		}
		if st.completed > 0 {
			lines = append(lines, fmt.Sprintf("%d files completed", st.completed))
		}
		return lines
	case stopCanceled:
		return []string{"Interrupted. Partial files kept for resume."}
	default:
		if subject == "" {
			subject = "Installation"
		}
		return []string{subject + " failed."}
	}
}

// sink is the rendering back end. The renderer owns the state and the view
// model; the sink only puts bytes on a stream. Every method is called with
// the renderer's mutex held — the event deliverer and the repaint loop are
// the only callers, so a sink needs no lock of its own. The paths crossing
// this boundary are display paths — relative to the install root once the
// plan has handed it over; diagnostics keep their absolute text.
type sink interface {
	// info renders a short-lived informational line. TTY: it becomes the
	// frame's transient message row on the next repaint; log: stdout now.
	info(text string)
	// diagnostic renders a warning or error. TTY: a Diagnostic transaction —
	// the frame comes down, the line lands on stderr, the frame goes back up.
	// Log: stderr now.
	diagnostic(text string)
	// taskStart / taskFinish render lifecycle lines. Log: one line each.
	// TTY: no-ops — the next frame already reflects the state.
	taskStart(index int, path string)
	taskFinish(index int, path string)
	// tick is called once per interval. TTY: draw the frame. Log: emit the
	// summary when the cadence asks for it.
	tick(vm viewModel)
	// finalize renders the run's terminal state and ends all output.
	finalize(reason stopReason, st runStats)
}

// runStats are the terminal-state counters: transferred completions, tasks
// the transfer resumed from a partial file, and tasks the transfer skipped
// authoritatively (the dynamic-skip case; plan-level skips never reach the
// queue at all).
type runStats struct {
	completed int
	resumed   int
	skipped   int
}

// renderer turns the transfer event stream plus the Progress sampling surface
// into a derived view model, and hands frames to a sink. It holds state, not
// terminal: it never writes directly — the coordinator owns the terminal.
//
// Progress is the numeric authority; the events are lifecycle and messages
// only. EventProgress carries no display state: the renderer never reads its
// Current, though the field stays in the event contract.
type renderer struct {
	source      progressSource // the numeric authority; never nil in production
	sink        sink
	activeTasks map[string]*renderTask
	stopped     chan struct{}

	stop           chan struct{}
	now            func() time.Time // swappable for tests
	bar            *progress.Bar
	installRoot    string // the plan's semantic install root; "" until handed over
	message        string // the latest transient info/success line
	order          []string
	finishedCount  int
	startedBytes   int64 // Σ started tasks' totals, accumulated at TaskStart
	resumed        int   // resumed tasks, counted from the explicit marker alone
	skippedDynamic int   // transfer-side skips, counted from the explicit marker alone
	interval       time.Duration

	mu       sync.Mutex
	unit     uint32
	finalize bool // Stop ran; further Stops are no-ops
	started  bool // Start ran; Stop waits for the loop only when it did
}

// renderTask is one active task's view: its path, when the renderer first saw
// it (the session average counts from there) and its sample window. The byte
// counts are NOT stored — they are read from Progress at view-model build
// time, so there is exactly one numeric source.
type renderTask struct {
	path   string
	start  time.Time
	window rateWindow
}

func newRenderTask(path string, now time.Time) *renderTask {
	return &renderTask{path: path, start: now, window: rateWindow{cap: 100}}
}

// viewModel is the derived presentation state: everything the frame or the
// log summary shows, computed at one instant.
type viewModel struct {
	message   string
	tasks     []taskRow
	active    int // running tasks
	queued    int // not yet started (snapshot − started)
	resumed   int // resumed tasks seen so far (explicit marker count only)
	rate      float64
	remaining int64
	etaSecs   float64
	etaValid  bool
}

// taskRow is one active task's derived numbers.
type taskRow struct {
	path  string
	index int // 1-based, TaskStart arrival order, stable across frames
	pct   float64
	done  int64
	total int64
	rate  float64
}

// rateWindow is the sliding byte window behind the instantaneous rate: at most
// 10 seconds of samples, capped at 100 points.
type rateWindow struct {
	points [][2]int64 // (unixNano, cumulative installed bytes)
	cap    int
}

func (w *rateWindow) add(t time.Time, bytes int64) {
	w.points = append(w.points, [2]int64{t.UnixNano(), bytes})
	// Two trim conditions: the slope only spans the last 10 seconds, and at
	// most 100 samples are kept.
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
// now — the query-time trim: the samples a stall has aged out must not keep
// feeding a slope.
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
// buffer and the next attempt starts from the chunk's offset again — so the old
// samples are dropped and the window restarts from this one. Never a negative
// slope, never a sample clamped back to its predecessor.
func (w *rateWindow) addSample(now time.Time, value int64) {
	if newest, ok := w.newest(); ok && value < newest {
		w.reset()
	}
	w.add(now, value)
}

// rate is the bytes-per-second slope over the last 10 seconds counted from
// now — the query time, not the newest sample's time. A window that holds
// fewer than two live samples has no slope to report and returns zero; the
// task falls back to its session average. Callers hold the renderer's mutex.
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

// rate is the task's download rate in bytes per second: the slope of its own
// window, or its session average when the window holds fewer than two live
// samples. done is the task's current sampled byte count, read from Progress by
// the caller. Callers hold the mutex.
func (t *renderTask) rate(now time.Time, done int64) float64 {
	if t.window.samples(now) >= 2 {
		return t.window.rate(now)
	}
	elapsed := now.Sub(t.start).Seconds()
	if elapsed <= 0 {
		return 0
	}
	return float64(done) / elapsed
}

// newRenderer wires a renderer over a sink. source is the numeric authority
// and is required: the display has exactly one numeric feed.
func newRenderer(sink sink, bar *progress.Bar, interval time.Duration, source progressSource) *renderer {
	if interval <= 0 {
		interval = 100 * time.Millisecond
	}
	return &renderer{
		bar:         bar,
		interval:    interval,
		now:         time.Now,
		source:      source,
		sink:        sink,
		activeTasks: map[string]*renderTask{},
		stop:        make(chan struct{}),
		stopped:     make(chan struct{}),
	}
}

// Start runs the repaint loop until Stop. A second Start is a no-op, and so
// is a Start after Stop: the run is finalized, its terminal state is on
// screen, and a ticker launched then would never see a stop signal again —
// every later Stop refuses itself on the finalize flag.
func (r *renderer) Start() {
	r.mu.Lock()
	if r.started || r.finalize {
		r.mu.Unlock()
		return
	}
	r.started = true
	r.mu.Unlock()
	go func() {
		defer close(r.stopped)
		ticker := time.NewTicker(r.interval)
		defer ticker.Stop()
		for {
			select {
			case <-r.stop:
				return
			case <-ticker.C:
				r.tick()
			}
		}
	}()
}

// Stop ends the repaint loop and emits the run's terminal state. It covers
// every exit path — success, error, cancellation and panic (the caller's
// defer reaches it with whatever reason was current) — but it never swallows
// anything: a panic propagates after the cleanup, exactly as Go would. A
// second Stop is a no-op, and a Stop without a Start (nothing to wait for)
// finalizes synchronously instead of blocking on the loop that never ran.
func (r *renderer) Stop(reason stopReason) {
	r.mu.Lock()
	if r.finalize {
		r.mu.Unlock()
		return
	}
	r.finalize = true
	wasStarted := r.started
	r.mu.Unlock()

	if wasStarted {
		close(r.stop)
		<-r.stopped
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.sink.finalize(reason, r.stats())
}

// stats snapshots the terminal-state counters. Callers hold the mutex.
func (r *renderer) stats() runStats {
	return runStats{completed: r.finishedCount, resumed: r.resumed, skipped: r.skippedDynamic}
}

// SetInstallRoot records the plan's semantic install root (the core seam of
// the same name calls this after BuildPlan). Task rows display paths relative
// to it; without it they keep the absolute form, which is never wrong, only
// verbose.
func (r *renderer) SetInstallRoot(path string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.installRoot = path
}

// displayPath strips the install root from a destination. A path outside the
// root (or before the root is known) is returned unchanged.
func (r *renderer) displayPath(path string) string {
	if r.installRoot == "" {
		return path
	}
	rel := strings.TrimPrefix(path, r.installRoot+"/")
	if rel == path {
		return path
	}
	return rel
}

// OnEvent implements transfer.Observer. The call is serial — transfer delivers
// through one goroutine — while the repaint loop reads the same state, hence
// the mutex. Progress events are lifecycle noise here: the numeric authority is
// Progress, so they are dropped.
func (r *renderer) OnEvent(ev transfer.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch ev.Kind {
	case transfer.EventTaskStart:
		// The task's total is published before TaskStart is emitted, so the
		// pending-byte side can accumulate it here and stays correct after
		// finished tasks leave the active model.
		r.activeTasks[ev.Path] = newRenderTask(ev.Path, r.now())
		r.order = append(r.order, ev.Path)
		if total, ok := r.source.Total(ev.Path); ok {
			r.startedBytes += total
		}
		r.sink.taskStart(len(r.order), r.displayPath(ev.Path))
	case transfer.EventTaskFinish:
		// The task leaves the active model; the row numbering is stable
		// because r.order keeps every started path.
		delete(r.activeTasks, ev.Path)
		r.finishedCount++
		r.sink.taskFinish(r.indexOf(ev.Path), r.displayPath(ev.Path))
	case transfer.EventMessageInfo, transfer.EventMessageSuccess:
		// The explicit resume/skip markers are counters, not display
		// material: N identical lifecycle records aggregate. Their event text
		// is never shown, and a resume is recognised ONLY by the marker —
		// never by inferring an event sequence (decision 1).
		if transfer.IsResumeMessage(ev.Text) {
			r.resumed++
			break
		}
		if transfer.IsSkipMessage(ev.Text) {
			r.skippedDynamic++
			break
		}
		r.message = ev.Text
		r.sink.info(ev.Text)
	case transfer.EventMessageWarning, transfer.EventMessageError:
		r.sink.diagnostic(ev.Text)
	default:
		// EventProgress and anything else: lifecycle noise, no display state.
	}
}

// indexOf is a path's 1-based display row: its position in the TaskStart
// arrival order. Callers hold the mutex.
func (r *renderer) indexOf(path string) int {
	for i, p := range r.order {
		if p == path {
			return i + 1
		}
	}
	return len(r.order)
}

// tick is one repaint: build the view model at a single instant, hand it to
// the sink. After Stop the run's terminal state is on screen, so a repaint
// would smear it — the tick refuses, the same contract the coordinator's
// stopped flag enforces one layer down.
func (r *renderer) tick() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finalize {
		return
	}
	r.sink.tick(r.buildVM(r.now()))
}

// buildVM derives the view model. Callers hold the mutex.
//
// The ETA is the wall-clock estimate for the whole task set: remaining =
// pendingBytes + Σ(active total − sampled bytes), divided by the aggregate rate
// — with remaining==0 taking priority over the rate==0 omission, so a finished
// run shows 0s rather than nothing.
func (r *renderer) buildVM(now time.Time) viewModel {
	vm := viewModel{message: r.message, resumed: r.resumed}
	var activeRemaining int64
	for i, path := range r.order {
		t := r.activeTasks[path]
		if t == nil {
			continue // finished: left the active model, numbering stays stable
		}
		var done, total int64
		if v, ok := r.source.Bytes(path); ok {
			done = v
		}
		if v, ok := r.source.Total(path); ok {
			total = v
		}
		// Display-layer defensive clamps: the sampled
		// counter never leaves [0, total].
		if done < 0 {
			done = 0
		}
		if total > 0 && done > total {
			done = total
		}
		t.window.addSample(now, done)
		rate := t.rate(now, done)
		vm.rate += rate
		vm.active++
		activeRemaining += max(total-done, 0)
		pct := 0.0
		if total > 0 {
			pct = float64(done) / float64(total)
		}
		vm.tasks = append(vm.tasks, taskRow{
			index: i + 1, path: r.displayPath(path), pct: pct, done: done, total: total, rate: rate,
		})
	}

	// The pending side comes from the run's snapshot minus what this renderer
	// has seen start; a subtract that would go negative is a state
	// disagreement, not a number worth showing, so it clamps at zero.
	// startedBytes was accumulated at TaskStart, so finished tasks leaving the
	// active model do not corrupt it.
	vm.queued = 0
	pendingBytes := int64(0)
	if tasks, bytes, ok := r.source.Queue(); ok {
		vm.queued = max(tasks-len(r.order), 0)
		pendingBytes = max(bytes-r.startedBytes, 0)
	}
	vm.remaining = pendingBytes + activeRemaining

	switch {
	case vm.remaining == 0:
		vm.etaSecs, vm.etaValid = 0, true
	case vm.rate == 0:
		vm.etaValid = false
	default:
		vm.etaSecs, vm.etaValid = float64(vm.remaining)/vm.rate, true
	}
	return vm
}

// ttySink renders frames through the terminal coordinator. The coordinator is
// the only terminal writer; this sink only composes frames.
type ttySink struct {
	coord  *terminalCoordinator
	bar    func(cells int, fraction float64) string
	width  func() int
	height func() int
	// subject names the run for the closing line ("Installation", "Download").
	subject string
	unit    uint32
}

func (s *ttySink) info(string)            {} // the frame's message row shows it
func (s *ttySink) taskStart(int, string)  {} // the next frame reflects it
func (s *ttySink) taskFinish(int, string) {}
func (s *ttySink) diagnostic(text string) { s.coord.writeErr(text) }

func (s *ttySink) tick(vm viewModel) {
	lines := layoutFrame(vm, s.width(), s.height(), s.bar, s.unit)
	s.coord.drawFrame(lines)
}

func (s *ttySink) finalize(reason stopReason, st runStats) {
	s.coord.finalize(finalLines(reason, st, s.subject))
}

// logSink is the non-TTY back end: append-only stable lines, no ANSI, no
// cursor sequences, no progress frames. Progress events never become lines;
// only lifecycle and message events do.
type logSink struct {
	lastSummary   time.Time
	out           io.Writer
	errOut        io.Writer
	now           func() time.Time
	subject       string // names the run for the closing line
	finishedSince int
	unit          uint32
}

func (s *logSink) info(text string)             { fmt.Fprintln(s.out, text) }
func (s *logSink) diagnostic(text string)       { fmt.Fprintln(s.errOut, text) }
func (s *logSink) taskStart(i int, path string) { fmt.Fprintf(s.out, "#%d %s\n", i, path) }
func (s *logSink) taskFinish(i int, path string) {
	fmt.Fprintf(s.out, "#%d %s: Finished\n", i, path)
	s.finishedSince++
}

// tick emits the summary when the cadence asks for it: at least 10 seconds
// since the last summary OR at least 10 finishes since it — whichever comes
// first — and both thresholds reset on every emit. The 10-task count is a
// maximum increment, not a fixed rhythm.
func (s *logSink) tick(vm viewModel) {
	now := s.now()
	if now.Sub(s.lastSummary) < 10*time.Second && s.finishedSince < 10 {
		return
	}
	line := "Rate " + util.RateString(vm.rate, s.unit) +
		" · " + util.SizeString(uint64(vm.remaining), s.unit) + " remaining"
	if vm.etaValid {
		line += " · ETA " + util.EtaString(int64(vm.etaSecs))
	}
	if vm.resumed > 0 {
		line += fmt.Sprintf(" · %d resuming", vm.resumed)
	}
	fmt.Fprintf(s.out, "%d active · %d queued · %s\n", vm.active, vm.queued, line)
	s.lastSummary = now
	s.finishedSince = 0
}

func (s *logSink) finalize(reason stopReason, st runStats) {
	for _, line := range finalLines(reason, st, s.subject) {
		if reason == stopCompleted {
			fmt.Fprintln(s.out, line)
		} else {
			fmt.Fprintln(s.errOut, line)
		}
	}
}
