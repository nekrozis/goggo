package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"

	"github.com/nekrozis/goggo/internal/config"
	"github.com/nekrozis/goggo/internal/core"
	"github.com/nekrozis/goggo/internal/transfer"
	"github.com/nekrozis/goggo/internal/util"
)

// outcome is how a command line ended. It is the ONLY thing exitCode turns into
// a number (review CLI1 §8): the contract — 0 success, 1 operational failure,
// 2 usage failure, 130 interrupted — lives in exactly one place, so no code path
// can invent a fifth meaning for a code.
type outcome uint8

const (
	outcomeOK outcome = iota
	outcomeUsageFailure
	outcomeOperationFailure
	outcomeInterrupted
)

// exitCode is the single exit-code authority.
func exitCode(o outcome) int {
	switch o {
	case outcomeOK:
		return 0
	case outcomeUsageFailure:
		return 2
	case outcomeInterrupted:
		return 130
	default:
		return 1
	}
}

// outcomeForError classifies a failure: everything the parser refuses is a usage
// failure, everything else is an operational one.
func outcomeForError(err error) outcome {
	if isUsageError(err) {
		return outcomeUsageFailure
	}
	return outcomeOperationFailure
}

// stopOutcome maps the install lifecycle's terminal reason (review UI1 v3 §6.E)
// onto the same contract.
func stopOutcome(reason stopReason) outcome {
	switch reason {
	case stopCompleted:
		return outcomeOK
	case stopCanceled:
		return outcomeInterrupted
	default:
		return outcomeOperationFailure
	}
}

// newConfig resolves the XDG roots (the single entry point for path
// resolution) and builds the defaults from them.
func newConfig() (config.Config, error) {
	configHome, err := util.ConfigHome()
	if err != nil {
		return config.Config{}, err
	}
	cacheHome, err := util.CacheHome()
	if err != nil {
		return config.Config{}, err
	}
	return config.NewConfig(configHome, cacheHome), nil
}

// Run executes one command line and returns the process exit code.
//
// It performs no process-level work: all input and output goes through the
// streams it is given, so the whole front end is testable and nothing can reach
// the real terminal. The command vocabulary, the option acceptance and the exit
// codes are the ones the parser and the command tree define; this function only
// decides which command runs (review CLI1 §6).
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	cfg, err := newConfig()
	if err != nil {
		return fail(stderr, err)
	}
	inv, err := parseArgs(args, cfg)
	if err != nil {
		return fail(stderr, err)
	}
	return exitCode(dispatch(inv, stdin, stdout, stderr))
}

// fail prints one diagnostic and returns the exit code its class implies.
func fail(w io.Writer, err error) int {
	fmt.Fprintf(w, "Error: %v\n", err)
	return exitCode(outcomeForError(err))
}

// sessionRequest is the ONE place a command's declared session class becomes the
// request core acts on (review CLI1 §7). The chain is short and has no second
// source of truth: the tree declares the class, the parser copies it into the
// invocation, and this function turns it into what OpenWith enforces.
//
// interactive is the terminal answer (ui.IsTerminal), and it conditions only the
// implicit login: without a terminal there is nobody to answer its prompts, so
// such a run must fail with the actionable error instead. An explicit login is
// not conditioned on it — its flow has a non-interactive branch of its own, and
// stdin may still carry the browser callback URL (review S4, ruling 2).
//
// An undeclared class is a broken tree, not user input. It panics rather than
// silently becoming "no session needed", which would let a command that needs
// the account run unauthenticated (review S4).
func sessionRequest(class sessionClass, interactive bool) core.SessionRequest {
	switch class {
	case sessionNone:
		return core.SessionRequest{}
	case sessionRequired:
		return core.SessionRequest{Required: true}
	case sessionImplicitLogin:
		return core.SessionRequest{Required: true, AllowLogin: interactive}
	case sessionExplicitLogin:
		return core.SessionRequest{AllowLogin: true}
	}
	panic("unhandled session class: " + class.String())
}

// dispatch runs one parsed invocation. The order below is the CLI's own: meta
// answers first (D18), then the commands that need no session, then everything
// that does.
func dispatch(inv invocation, stdin io.Reader, stdout, stderr io.Writer) outcome {
	ui := newConsole(stdin, stdout, stderr)
	ctx := context.Background()

	switch inv.meta {
	case metaHelp:
		usage(stdout, inv.helpPath)
		return outcomeOK
	case metaVersion:
		renderVersion(stdout)
		return outcomeOK
	}

	if inv.cmd == cmdNone {
		// A bare invocation shows the CLI's surface and fails as a usage
		// error, so a script that forgot the command does not read the run as
		// a success.
		fmt.Fprintln(stderr, config.VersionString)
		usage(stderr, nil)
		return outcomeUsageFailure
	}

	// Commands that answer without a session, in the order they must be
	// considered: a mutation never outranks a query, and neither opens one
	// (auth logout must not: a Downloader flushes its cookie jar on Close,
	// which would write the removed cookie file straight back).
	switch inv.cmd {
	case cmdAuthStatus:
		d, err := core.Open(ctx, inv.cfg, ui, sessionRequest(inv.session, ui.IsTerminal()))
		if err != nil {
			return reportError(stderr, err)
		}
		if d.LoggedIn() {
			fmt.Fprintln(stdout, "Login status: Logged in")
			return outcomeOK
		}
		fmt.Fprintln(stdout, "Login status: Not logged in")
		return outcomeOperationFailure
	case cmdAuthLogout:
		if err := logout(inv.cfg, stdout); err != nil {
			return reportError(stderr, err)
		}
		return outcomeOK
	case cmdAuthLogin:
		// An explicit login always runs the flow, even with a usable session
		// stored: that is what asking to log in means (the upstream --login
		// does the same, main.cpp:679-683).
		//
		// It is set here, not in sessionRequest: SessionRequest says what the
		// run needs from the session, while "the user ran the login command" is
		// a fact about this command (review S4, ruling B).
		inv.cfg.Login = true
	}

	// The commands whose capability arrives in a later step of CLI1. They are
	// registered so their names are stable and their help exists, but they must
	// not pretend to have done something.
	// (Nothing is stubbed any more: S6 wired the last two, orphans check and
	// orphans remove.)
	// The destructive commands must not run without a way to authorize them: a
	// removal with no terminal to ask on and no --yes is a usage failure, and it
	// is answered before any session or network work happens (review CLI1 §8,
	// T12).
	if inv.cmd == cmdOrphansRemove && !inv.yes && !ui.IsTerminal() {
		return reportError(stderr, usagef(
			"orphans remove needs --yes when the input is not a terminal"))
	}

	// The sampling surface belongs to the one command that polls it: an install
	// run publishes its per-task byte counts into this registry and the renderer
	// reads it back. Every other command opens without one, which is the nil
	// case transfer skips entirely (review S-ETA2).
	var progress *transfer.Progress
	if inv.cmd == cmdInstall {
		progress = transfer.NewProgress()
	}

	// What this command asks of the session, decided once for every command
	// that opens one (review CLI1 §7).
	req := sessionRequest(inv.session, ui.IsTerminal())
	d, err := core.OpenWith(ctx, inv.cfg, ui, req, core.Dependencies{Progress: progress})
	if err != nil {
		return reportError(stderr, err)
	}
	defer func() { _ = d.Close() }()

	// Downloader::init (main.cpp:802-806): a usable access token is checked
	// before any command runs, and a failure stops the run.
	if err := d.Init(ctx); err != nil {
		return reportError(stderr, err)
	}

	switch inv.cmd {
	case cmdListGames, cmdListTags, cmdListWishlist:
		if err := renderList(ctx, d, listFormat(inv.cmd), stdout); err != nil {
			return reportError(stderr, err)
		}
		return outcomeOK

	case cmdShowBuilds, cmdShowManifest:
		// "show builds" lists a product's builds; "show manifest" shows one
		// build's manifest. Upstream folded both into one option whose meaning
		// changed with the argument (downloader.cpp:4833-4837); the split made
		// the intent explicit, so a manifest request without a build means the
		// build a plain install would pick (index 0), not "list them".
		build := inv.target.Build
		if inv.cmd == cmdShowManifest && build == "" {
			build = "0"
		}
		res, err := d.ShowBuilds(ctx, inv.target.Product, build)
		// The Linux support messages are rendered even when the run then fails:
		// the C++ source prints them to stdout and this port reports the missing
		// fallback on stderr afterwards (downloader.cpp:4858-4863).
		renderNotice(stdout, stderr, res.Notice)
		if err != nil {
			return reportError(stderr, err)
		}
		if res.Manifest != nil {
			if err := renderManifest(stdout, res.Manifest); err != nil {
				return reportError(stderr, err)
			}
			return outcomeOK
		}
		if err := renderBuilds(stdout, res.Builds); err != nil {
			return reportError(stderr, err)
		}
		return outcomeOK

	case cmdShowCDNs:
		res, err := d.ListCDNs(ctx, inv.target.Product, inv.target.Build)
		renderNotice(stdout, stderr, res.Notice)
		if err != nil {
			return reportError(stderr, err)
		}
		if err := renderCDNNames(stdout, res.Names); err != nil {
			return reportError(stderr, err)
		}
		return outcomeOK

	case cmdVerify:
		// A verification reads the same installation an install writes, so it
		// resolves its target exactly like one (review §13⑤) — and then only
		// observes: the plan is built in verify mode (no destructive work, no
		// free-space answer) and every expected file is classified. The plan's
		// own messages are rendered first, the way an install emits them as it
		// goes; on a plan failure that is all there is to show (review S5).
		req := core.NewInstallRequest(inv.cfg, inv.target.Product, inv.target.Build)
		res, err := d.Verify(ctx, req)
		for _, notice := range res.Notices {
			renderNotice(stdout, stderr, notice)
		}
		if err != nil {
			return reportError(stderr, err)
		}
		return renderVerify(stdout, stderr, res)

	case cmdOrphansCheck:
		return ui.runOrphansCheck(ctx, d, inv, stdout, stderr)

	case cmdOrphansRemove:
		return ui.runOrphansRemove(ctx, d, inv, stdout, stderr)

	case cmdInstall:
		// The lifecycle has one owner and one order (review UI1 v3 §6.E):
		// signal context → renderer start → the install with Stop deferred →
		// signal restore. Stop covers every exit path — error, cancellation and
		// panic — but never swallows: a panic propagates after the cleanup, as
		// Go would.
		req := core.NewInstallRequest(inv.cfg, inv.target.Product, inv.target.Build)
		return stopOutcome(ui.runInstall(ctx, d, req, inv.cfg, progress))
	}

	// Unreachable while the command tree and this switch agree; keeping it an
	// operational failure means a future command that forgets a case fails
	// loudly instead of exiting 0.
	return reportError(stderr, fmt.Errorf("%s has no handler", inv.cmd.path()))
}

// reportError prints a diagnostic to stderr and classifies it.
func reportError(w io.Writer, err error) outcome {
	fmt.Fprintf(w, "Error: %v\n", err)
	return outcomeForError(err)
}

// listFormat maps the list commands onto the catalogue's format mask.
func listFormat(id commandID) uint32 {
	switch id {
	case cmdListGames:
		return config.ListFormatGames
	case cmdListTags:
		return config.ListFormatTags
	case cmdListWishlist:
		return config.ListFormatWishlist
	}
	return 0
}

// runInstall drives one install operation and returns its result. The reason
// is fixed BEFORE the deferred Stop reads it, so a panic or an early return
// finalizes with the right state (review UI1 v3, constraint 6): the result
// variable starts at failed, so an unwound panic finalizes as failed and then
// propagates — Stop cleans up, nothing is swallowed.
func (c *console) runInstall(ctx context.Context, d *core.Downloader, req core.InstallRequest, cfg config.Config, progress *transfer.Progress) stopReason {
	ctx, stopSignal := signal.NotifyContext(ctx, os.Interrupt)
	c.attachInstallUI(cfg, progress)

	result := stopFailed
	var installErr error
	c.renderer.Start()
	func() {
		defer func() { c.renderer.Stop(result) }()
		installErr = d.Install(ctx, req)
		result = classifyInstallResult(installErr, ctx)
	}()
	// The rendering is finalized; only now does SIGINT regain its default
	// behavior, so a second Ctrl+C during any remaining cleanup force-kills
	// instead of being swallowed by the context (review UI1 v3, decision 4).
	stopSignal()
	c.endInstallScope()

	// The failure detail goes to stderr after the frame is gone; cancellation
	// already announced itself through the terminal state.
	if installErr != nil && result == stopFailed {
		fmt.Fprintf(c.errOut, "Error: %v\n", installErr)
	}
	return result
}

// classifyInstallResult maps an Install outcome onto the run's terminal
// state. Cancellation is recognized from the error chain or the context —
// both are the same "the run was interrupted" fact.
func classifyInstallResult(err error, ctx context.Context) stopReason {
	if err == nil {
		return stopCompleted
	}
	if errors.Is(err, context.Canceled) || ctx.Err() != nil {
		return stopCanceled
	}
	return stopFailed
}
