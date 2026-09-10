package core

import (
	"context"
	"io"

	"github.com/nekrozis/goggo/internal/webapi"
)

// Console is the whole of the front end this package talks to.
//
// It is declared consumer-side on purpose: the CLI console implements it
// structurally, so orchestration never imports the front end (internal/cli
// imports this package, not the other way round). A test provides its own
// implementation instead of a terminal.
//
// Stream policy follows the C++ source: prompts and status lines go to ErrOut,
// program output to Out (downloader.cpp:267,270, website.cpp:519,524,614-617).
type Console interface {
	// Out carries program output: the product list a selection is made from,
	// for instance.
	Out() io.Writer

	// ErrOut carries prompts and status lines.
	ErrOut() io.Writer

	// IsTerminal reports whether prompts can be answered here. It maps the C++
	// isatty(STDIN_FILENO) test that selects between prompting and the headless
	// login branch (downloader.cpp:256).
	IsTerminal() bool

	// PromptEmail asks for the account e-mail.
	PromptEmail() (string, error)

	// PromptPassword asks for the account password.
	PromptPassword() (string, error)

	// ResolveChallenge finishes an interactive login: it prints what the user
	// has to do, reads the answer and hands it back to webapi. The challenge is
	// consumed exactly once by webapi, so a failure here is reported rather
	// than retried.
	ResolveChallenge(ctx context.Context, web *webapi.Client, ch *webapi.LoginChallenge) error

	// SelectProduct prints the numbered product list and asks for an index
	// (downloader.cpp:3865-3886). An error means the selection cannot be made
	// here — no terminal, or unreadable input — and the caller reports it the
	// way the C++ source reports an unanswerable prompt.
	SelectProduct(items []string) (int, error)
}
