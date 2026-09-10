// Package cli is the command-line front end: it parses the supported options
// into a config.Config, drives the orchestration in internal/core and renders
// the supported outputs.
//
// Layering (review lock, S12; S17 moved the orchestration out): this package
// owns flag parsing, the console and rendering. Everything else lives where it
// belongs — orchestration and command execution in internal/core, protocol work
// in internal/webapi, credential persistence in internal/auth, transport in
// internal/httpx and listing assembly in internal/catalog. All input and output
// goes through the io.Reader/Writer pair passed to Run, so nothing touches the
// real terminal and tests can drive the whole front end.
package cli

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"

	"golang.org/x/term"
)

// console carries the streams and the buffered reader shared by the prompts. It
// is the front end internal/core talks to, and satisfies core.Console
// structurally (see the assertion in the tests).
//
// rawIn is the reader exactly as it was handed in: the password prompt needs it
// to ask whether the input is a real terminal, which the buffered reader hides.
// All four fields are interface values, so their order does not affect the size.
type console struct {
	in     *bufio.Reader
	rawIn  io.Reader
	out    io.Writer
	errOut io.Writer
}

func newConsole(in io.Reader, out, errOut io.Writer) *console {
	return &console{in: bufio.NewReader(in), rawIn: in, out: out, errOut: errOut}
}

// Out and ErrOut expose the two streams; see core.Console for the stream policy.
func (c *console) Out() io.Writer    { return c.out }
func (c *console) ErrOut() io.Writer { return c.errOut }

// terminalFd returns the descriptor of the input stream when that stream is a
// real terminal. It is the single place that answers "can a prompt be answered
// here?", so the password prompt and the login flow cannot disagree.
//
// rawIn is the reader exactly as it was handed in: io.Reader never exposes a
// descriptor, so anything that is not an *os.File — tests, pipes, redirection —
// is not a terminal.
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

// IsTerminal reports whether prompts can be answered on this input. It maps the
// C++ isatty(STDIN_FILENO) test that selects between prompting and the headless
// login branch (downloader.cpp:256).
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

// SelectProduct prints the numbered product list and asks for an index
// (downloader.cpp:3865-3886).
//
// The order is the C++ one: the list goes to stdout FIRST, and only then is the
// terminal checked, so a non-interactive run still sees the candidates it could
// not choose from. The prompt and the retry message go to stderr.
//
// Difference (recorded): where the C++ source prints the std::stoi exception
// text ("stoi") on an unparsable answer, this port says what to type.
func (c *console) SelectProduct(items []string) (int, error) {
	fmt.Fprintln(c.out, "Select product:")
	for i, name := range items {
		fmt.Fprintf(c.out, "%d: %s\n", i, name)
	}
	if !c.IsTerminal() {
		return 0, fmt.Errorf("no terminal to read the selection from")
	}
	for {
		fmt.Fprint(c.errOut, "> ")
		line, err := c.readLine()
		if err != nil {
			return 0, err
		}
		if n, err := strconv.Atoi(strings.TrimSpace(line)); err == nil && n >= 0 && n < len(items) {
			return n, nil
		}
		fmt.Fprintln(c.errOut, "invalid selection")
	}
}
