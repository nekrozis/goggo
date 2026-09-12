package cli

import (
	"fmt"
	"io"
	"strings"
	"sync"
)

// terminalCoordinator is the single terminal-visible writer for an install
// run's TTY lifetime (review UI1 v3 §6.C): every byte that reaches the
// terminal while the run is live goes through one of its three transactions —
// Frame, Diagnostic, Stop — so the cursor always has exactly one owner.
//
// The mutex alone is not the ownership: the coordinator is the ownership. It
// works because the console routes ALL of its output through it for the
// install's lifetime (console.Out/ErrOut hand back coordinator writers, and
// every core write flows through core.Console → those methods), so no third
// path can interleave a write between the erase and the redraw. Login prompts
// and non-install commands run outside that lifetime and write directly.
//
// Each transaction ends at a known cursor state: the frame is on screen and
// the cursor sits just below it. The next transaction erases from there, so
// the row arithmetic never depends on where an earlier write left the cursor.
// Layout guarantees the frame never wraps (layout.go), which is what makes
// the erase's row count trustworthy.
type terminalCoordinator struct {
	mu      sync.Mutex
	out     io.Writer
	errOut  io.Writer
	frame   []string // the frame currently on screen
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
func (c *terminalCoordinator) drawFrame(lines []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopped {
		return
	}
	c.erase()
	for _, line := range lines {
		fmt.Fprintln(c.out, line)
	}
	c.frame = lines
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
