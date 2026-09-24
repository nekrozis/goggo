package cli

import (
	"fmt"
	"io"
	"strings"
	"sync"
)

// terminalCoordinator is the single terminal-visible writer for an install run's
// TTY lifetime: every byte that reaches the terminal while the run is live goes
// through one of its three transactions — Frame, Diagnostic, Stop — so the
// cursor always has exactly one owner.
//
// That holds only because the console routes ALL of its output through it for
// the install's lifetime (console.Out/ErrOut hand back coordinator writers);
// login prompts and non-install commands run outside that lifetime and write
// directly, so no third path can interleave a write between erase and redraw.
//
// Each transaction ends at a known cursor state: the frame is on screen and the
// cursor sits just below it, so the row arithmetic never depends on where an
// earlier write left the cursor. Layout guarantees the frame never wraps
// (layout.go), which is what makes the erase's row count trustworthy.
//
// A prompt takes the terminal over through suspend/resume: the frame comes down
// and stays down, so the cursor stops moving under the user's input and the
// answer is not painted over.
type terminalCoordinator struct {
	out     io.Writer
	errOut  io.Writer
	frame   []string // the frame currently on screen
	pending []string // the frame a paused coordinator paints on resume
	mu      sync.Mutex
	paused  bool
	stopped bool
}

func newTerminalCoordinator(out, errOut io.Writer) *terminalCoordinator {
	return &terminalCoordinator{out: out, errOut: errOut}
}

// erase clears the painted frame and parks the cursor at its top-left cell.
func (c *terminalCoordinator) erase() {
	if len(c.frame) > 0 {
		fmt.Fprintf(c.out, "\033[%dA\r\033[J", len(c.frame))
	}
}

// redraw repaints the stored frame below the cursor.
func (c *terminalCoordinator) redraw() {
	for _, line := range c.frame {
		fmt.Fprintln(c.out, line)
	}
}

// drawFrame is the Frame transaction: erase what is on screen, paint the new
// frame, leave the cursor just below it.
//
// While a prompt holds the terminal the frame is only remembered: painting it
// would move the cursor away from the answer being typed. The remembered frame
// is what resume puts back.
func (c *terminalCoordinator) drawFrame(lines []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopped {
		return
	}
	if c.paused {
		c.pending = lines
		return
	}
	c.erase()
	for _, line := range lines {
		fmt.Fprintln(c.out, line)
	}
	c.frame = lines
}

// suspend hands the terminal to a prompt: the frame comes down and no later
// frame is painted until resume. The frame in flight is kept, so resume has
// something to put back even if the prompt is answered before the next tick.
//
// The state lives here, not in the caller: a prompt that stopped the renderer
// itself would be a second owner of the frame's lifetime.
func (c *terminalCoordinator) suspend() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopped || c.paused {
		return
	}
	c.erase()
	c.pending = c.frame
	c.frame = nil
	c.paused = true
}

// resume takes the terminal back and paints the newest frame it was handed
// while the prompt was up.
func (c *terminalCoordinator) resume() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopped || !c.paused {
		return
	}
	c.paused = false
	for _, line := range c.pending {
		fmt.Fprintln(c.out, line)
	}
	c.frame = c.pending
	c.pending = nil
}

// writeOut and writeErr are the Diagnostic transactions: the frame comes down,
// the line lands on its stream, the frame goes back up. Used for diagnostics
// that must survive in the scrollback (warnings, errors) and for core notices
// routed through the console during the install.
func (c *terminalCoordinator) writeOut(line string) { c.transaction(c.out, line) }
func (c *terminalCoordinator) writeErr(line string) { c.transaction(c.errOut, line) }

func (c *terminalCoordinator) transaction(w io.Writer, line string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopped {
		return
	}
	if c.paused {
		// A prompt owns the terminal and the frame is already down: the line
		// goes straight to its stream, because there is no frame to take down
		// and put back.
		fmt.Fprintln(w, line)
		return
	}
	c.erase()
	fmt.Fprintln(w, line)
	c.redraw()
}

// finalize is the Stop transaction: the frame comes down for good, the final
// lines are written, and every later write is refused. The cursor rests after
// the final state.
func (c *terminalCoordinator) finalize(lines []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopped {
		return
	}
	c.erase()
	for _, line := range lines {
		fmt.Fprintln(c.out, line)
	}
	c.frame = nil
	c.stopped = true
}

// writer returns an io.Writer whose every Write is one transaction on the
// given stream. This is the seam console.Out/ErrOut hand out during the
// install's TTY lifetime: a caller that only knows "io.Writer" still lands on
// the coordinator. Callers write whole lines (the codebase prints through
// fmt.Fprintln); a Write without a trailing newline is still treated as one
// line.
func (c *terminalCoordinator) writer(toErr bool) io.Writer {
	return coordinatorWriter{c: c, toErr: toErr}
}

type coordinatorWriter struct {
	c     *terminalCoordinator
	toErr bool
}

func (w coordinatorWriter) Write(p []byte) (int, error) {
	line := strings.TrimRight(string(p), "\r\n")
	if line == "" {
		return len(p), nil
	}
	if w.toErr {
		w.c.writeErr(line)
	} else {
		w.c.writeOut(line)
	}
	return len(p), nil
}
