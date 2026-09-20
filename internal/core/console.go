package core

import (
	"context"
	"io"

	"github.com/nekrozis/goggo/internal/transfer"
	"github.com/nekrozis/goggo/internal/webapi"
)

// Console is the whole of the front end this package talks to.
//
// It is declared consumer-side on purpose: the CLI console implements it
// structurally, so orchestration never imports the front end (internal/cli
// imports this package, not the other way round). A test provides its own
// implementation instead of a terminal.
//
// Stream policy: prompts and status lines go to ErrOut, program output to Out.
type Console interface {
	// Out carries program output: the product list a selection is made from,
	// for instance.
	Out() io.Writer

	// ErrOut carries prompts and status lines.
	ErrOut() io.Writer

	// IsTerminal reports whether prompts can be answered here. It selects
	// between prompting and the headless login branch.
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

	// SelectProduct prints the numbered product list and asks for an index. An
	// error means the selection cannot be made here — no terminal, or
	// unreadable input — and the caller reports it as an unanswerable prompt.
	SelectProduct(items []string) (int, error)
}

// transferEventSink is the optional ability of a front end to consume the full
// transfer event stream, the per-task progress included. The install run
// checks for it with a type assertion and hands the whole stream to a front
// end that has it; a plain Console keeps the message-only path (D75).
// The interface stays unexported on purpose: the CLI satisfies it structurally
// and core's public surface does not grow.
type transferEventSink interface {
	OnEvent(transfer.Event)
}

// installRootSetter is the optional ability of a front end that renders task
// rows to receive the plan's semantic install root, so it can display task
// paths relative to it. The install run hands res.InstallPath over after
// BuildPlan — the ONE root with the right semantics (the %install_dir%
// template resolved; not cfg.Directories.Directory, which a subdir template
// can differ from). Like transferEventSink the interface itself stays
// unexported; the method is exported so the CLI can satisfy it structurally.
type installRootSetter interface {
	SetInstallRoot(path string)
}
