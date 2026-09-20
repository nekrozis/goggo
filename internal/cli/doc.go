// Package cli is the command-line front end: it parses a command line into a
// config.Config, drives the orchestration in internal/core and renders the
// output. All input and output goes through the io.Reader/Writer pair passed to
// Run, so nothing touches the real terminal and tests can drive the whole front
// end.
//
// Exit codes: 0 success, 1 operational failure, 2 usage failure, 130
// interrupted (run.go owns the mapping).
package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/nekrozis/goggo/internal/config"
	"github.com/nekrozis/goggo/internal/transfer"
	"github.com/nekrozis/goggo/internal/ui/progress"
	"golang.org/x/term"
)

// console carries the streams and the buffered reader shared by the prompts. It
// is the front end internal/core talks to, and satisfies core.Console
// structurally (see the assertion in the tests).
//
// rawIn is the reader exactly as it was handed in: the password prompt needs it
// to ask whether the input is a real terminal, which the buffered reader hides.
type console struct {
	in       *bufio.Reader
	rawIn    io.Reader
	out      io.Writer
	errOut   io.Writer
	renderer *renderer
	coord    *terminalCoordinator // non-nil only during an install's TTY lifetime
}

func newConsole(in io.Reader, out, errOut io.Writer) *console {
	return &console{in: bufio.NewReader(in), rawIn: in, out: out, errOut: errOut}
}

// Out and ErrOut expose the two streams; see core.Console for the stream policy.
//
// During an install's TTY lifetime both route through the terminal coordinator,
// so every core write — plan notices, deletions, SFC extraction — lands as a
// coordinator transaction and can never interleave with the live frame. Outside
// that lifetime — login prompts, list/builds output, usage — the streams are
// handed out directly.
func (c *console) Out() io.Writer {
	if c.coord != nil {
		return c.coord.writer(false)
	}
	return c.out
}

func (c *console) ErrOut() io.Writer {
	if c.coord != nil {
		return c.coord.writer(true)
	}
	return c.errOut
}

// attachInstallUI builds the renderer for one transferring run. The sink is
// chosen by the OUTPUT side — stdout is a terminal ⇒ live TTY frames through a
// coordinator; otherwise the append-only log sink — and the width and height
// come from that same descriptor, not from stdin, which stays reserved for the
// prompts. subject names the command for the closing line
// ("Installation", "Download"), so the frame cannot misname what it just ran.
func (c *console) attachInstallUI(cfg config.Config, source progressSource, subject string) {
	bar := progress.NewBar(cfg.Unicode, cfg.Color)
	interval := time.Duration(cfg.ProgressInterval) * time.Millisecond

	if out, ok := c.out.(*os.File); ok && term.IsTerminal(int(out.Fd())) {
		fd := int(out.Fd())
		width := func() int {
			w, _, err := term.GetSize(fd)
			if err != nil || w <= 0 {
				return 80
			}
			return w
		}
		height := func() int {
			_, h, err := term.GetSize(fd)
			if err != nil || h <= 0 {
				return 24
			}
			return h
		}
		c.coord = newTerminalCoordinator(c.out, c.errOut)
		c.renderer = newRenderer(&ttySink{
			coord:   c.coord,
			bar:     bar.Create,
			width:   width,
			height:  height,
			unit:    cfg.UnitFormat,
			subject: subject,
		}, bar, interval, source)
		return
	}
	c.renderer = newRenderer(&logSink{
		out:         c.out,
		errOut:      c.errOut,
		unit:        cfg.UnitFormat,
		now:         time.Now,
		subject:     subject,
		lastSummary: time.Now(),
	}, bar, interval, source)
}

// endInstallScope tears the coordinator routing down: the frame is finalized
// by renderer.Stop first, so afterwards both streams are plain again.
func (c *console) endInstallScope() { c.coord = nil }

// OnEvent hands the transfer event stream to the renderer. The method exists so
// core's optional-ability assertion finds the console capable: core knows no
// renderer type, only this behaviour.
func (c *console) OnEvent(ev transfer.Event) {
	if c.renderer != nil {
		c.renderer.OnEvent(ev)
	}
}

// SetInstallRoot reports the plan's semantic install root; the renderer
// displays task rows relative to it. A console without a live renderer ignores
// it — the root only matters to the progress display.
func (c *console) SetInstallRoot(path string) {
	if c.renderer != nil {
		c.renderer.SetInstallRoot(path)
	}
}

// terminalFd returns the descriptor of the input stream when that stream is a
// real terminal. It is the single place that answers "can a prompt be answered
// here?", so the password prompt and the login flow cannot disagree.
//
// io.Reader exposes no descriptor, so anything that is not an *os.File — tests,
// pipes, redirection — is not a terminal.
func (c *console) terminalFd() (int, bool) {
	f, ok := c.rawIn.(interface{ Fd() uintptr })
	if !ok {
		return 0, false
	}
	fd := int(f.Fd())
	if !term.IsTerminal(fd) {
		return 0, false
	}
	return fd, true
}

// IsTerminal reports whether prompts can be answered on this input.
func (c *console) IsTerminal() bool {
	_, ok := c.terminalFd()
	return ok
}

// readLine reads one line without its trailing newline.
func (c *console) readLine() (string, error) {
	line, err := c.in.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	for len(line) > 0 && (line[len(line)-1] == '\n' || line[len(line)-1] == '\r') {
		line = line[:len(line)-1]
	}
	return line, nil
}

// SelectProduct prints the numbered product list and asks for an index.
//
// The list goes to stdout FIRST, and only then is the terminal checked, so a
// non-interactive run still sees the candidates it could not choose from. The
// prompt and the retry message go to stderr, and an unparsable answer is
// answered with what to type rather than an error code.
//
// During an install the live frame owns the terminal, so the prompt suspends it
// first: the frame comes down and stops being painted, which is what keeps the
// candidate list from being erased by the next repaint. Outside an install
// there is no frame and the streams are the plain ones.
func (c *console) SelectProduct(items []string) (int, error) {
	if c.coord != nil {
		c.coord.suspend()
		defer c.coord.resume()
	}
	out, errOut := c.Out(), c.ErrOut()
	fmt.Fprintln(out, "Select product:")
	for i, name := range items {
		fmt.Fprintf(out, "%d: %s\n", i, name)
	}
	if !c.IsTerminal() {
		return 0, fmt.Errorf("no terminal to read the selection from")
	}
	for {
		fmt.Fprint(errOut, "> ")
		line, err := c.readLine()
		if err != nil {
			return 0, err
		}
		if n, err := strconv.Atoi(strings.TrimSpace(line)); err == nil && n >= 0 && n < len(items) {
			return n, nil
		}
		fmt.Fprintln(errOut, "invalid selection")
	}
}
