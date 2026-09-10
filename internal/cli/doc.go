// Package cli is the command-line front end: it parses the supported options
// into a config.Config, drives the session (cookies, token, refresh, login)
// and renders the supported list formats.
//
// Layering (review lock, S12): this package owns orchestration and I/O only.
// Protocol work stays in internal/webapi, credential persistence in
// internal/auth, transport in internal/httpx and listing assembly in
// internal/catalog. All input and output goes through the io.Reader/Writer
// pair passed to Run, so nothing touches the real terminal and tests can drive
// the whole front end.
//
// The session orchestration lives here temporarily: the C++ original keeps it
// in Downloader (downloader.cpp), which is ported in S17. When internal/core
// exists, session.go moves there wholesale.
package cli

import (
	"bufio"
	"io"

	"golang.org/x/term"
)

// console carries the streams and the buffered reader shared by the prompts.
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

// isTerminal reports whether prompts can be answered on this input. It maps the
// C++ isatty(STDIN_FILENO) test that selects between prompting and the headless
// login branch (downloader.cpp:256).
func (c *console) isTerminal() bool {
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
