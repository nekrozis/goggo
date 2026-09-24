package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"github.com/nekrozis/goggo/internal/config"
	"github.com/nekrozis/goggo/internal/core"
	"github.com/nekrozis/goggo/internal/transfer"
	"github.com/nekrozis/goggo/internal/util"
)

// outcome is how a command line ended. It is the ONLY thing exitCode turns into
// a number: the contract — 0 success, 1 operational failure,
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

// stopOutcome maps the install lifecycle's terminal reason
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
// the real terminal. This function only decides which command runs.
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return runWithDeps(args, stdin, stdout, stderr, core.Dependencies{})
}

// runWithDeps is Run with the one seam core already exposes: the network exit
// (Dependencies.HTTPTransport). Production passes the zero value; a test points
// every command at a local server and drives the real dispatcher end to end.
// The seam is what makes dispatch coverage observable without a network — the
// gap that let `auth login` fall into the no-handler default unnoticed.
func runWithDeps(args []string, stdin io.Reader, stdout, stderr io.Writer, deps core.Dependencies) int {
	cfg, err := newConfig()
	if err != nil {
		return fail(stderr, err)
	}
	inv, err := parseArgs(args, cfg)
	if err != nil {
		return fail(stderr, err)
	}
	return exitCode(dispatch(inv, stdin, stdout, stderr, deps))
}

// fail prints one diagnostic and returns the exit code its class implies.
func fail(w io.Writer, err error) int {
	fmt.Fprintf(w, "Error: %v\n", err)
	return exitCode(outcomeForError(err))
}

// sessionRequest is the ONE place a command's declared session class becomes the
// request core acts on.
//
// interactive conditions only the implicit login: without a terminal nobody can
// answer its prompts, so such a run must fail with the actionable error. An
// explicit login has a non-interactive branch of its own (stdin may still carry
// the browser callback URL), so it is never conditioned on the terminal.
//
// An undeclared class is a broken tree, not user input: it panics rather than
// silently becoming "no session needed", which would let a command that needs
// the account run unauthenticated.
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
// answers first, then the commands that need no session, then everything
// that does.
func dispatch(inv invocation, stdin io.Reader, stdout, stderr io.Writer, deps core.Dependencies) outcome {
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
		// OpenWith rather than Open: identical in production (Open is the
		// empty-deps call), and it lets the status report be tested through
		// the same transport seam every other command already uses.
		d, err := core.OpenWith(ctx, inv.cfg, ui, sessionRequest(inv.session, ui.IsTerminal()), deps)
		if err != nil {
			return reportError(stderr, err)
		}
		if d.LoggedIn() {
			// The two sessions are separate facts. This command never asks
			// the API anything, so a live local credential is reported as
			// unproven rather than as usable: no active probe, no inference.
			fmt.Fprintln(stdout, "Login status: Logged in")
			fmt.Fprintln(stdout, "API session: unknown")
			return outcomeOK
		}
		fmt.Fprintln(stdout, "Login status: Not logged in")
		// The first line is unchanged and so is the exit code; this line only
		// explains why the API credential could not be renewed. The web
		// session may differ — its state is not inferred here.
		if diag := d.APISessionDiag(); diag != "" {
			fmt.Fprintf(stdout, "API session: degraded (refresh failed: %s)\n", diag)
		}
		return outcomeOperationFailure
	case cmdAuthClear:
		if err := clearAuth(inv.cfg, stdout); err != nil {
			return reportError(stderr, err)
		}
		return outcomeOK
	case cmdManifestInspect:
		return runManifestInspect(inv, stdout, stderr)
	case cmdManifestVerify:
		return runManifestVerify(inv, stdout, stderr)
	case cmdManifestCreate:
		return runManifestCreate(inv, stdout, stderr)
	case cmdAuthLogin:
		// An explicit login always runs the flow, even with a usable session
		// stored: that is what asking to log in means.
		//
		// It is set here, not in sessionRequest: SessionRequest says what the
		// run needs from the session, while "the user ran the login command" is
		// a fact about this command.
		inv.cfg.Login = true
	}

	// The destructive commands must not run without a way to authorize them: a
	// removal with no terminal to ask on and no --yes is a usage failure, and it
	// is answered before any session or network work happens.
	if inv.cmd == cmdOrphansRemove && !inv.yes && !ui.IsTerminal() {
		return reportError(stderr, usagef(
			"orphans remove needs --yes when the input is not a terminal"))
	}

	// The sampling surface belongs to the commands that poll it: an install
	// or a download run publishes its per-task byte counts into this registry
	// and the renderer reads it back. Every other command opens without one,
	// which is the nil case transfer skips entirely.
	var progress *transfer.Progress
	if inv.cmd == cmdInstall || inv.cmd == cmdBackupDownload {
		progress = transfer.NewProgress()
	}

	// What this command asks of the session, decided once for every command
	// that opens one.
	req := sessionRequest(inv.session, ui.IsTerminal())
	// The sampling surface is the CLI's own, so it only fills the field when
	// this command publishes samples; an injected Progress (a test's) is left
	// alone otherwise.
	if progress != nil {
		deps.Progress = progress
	}
	d, err := core.OpenWith(ctx, inv.cfg, ui, req, deps)
	if err != nil {
		return reportError(stderr, err)
	}
	defer func() { _ = d.Close() }()

	// Downloader::init: a usable access token is checked
	// before any command runs, and a failure stops the run.
	// Commands declaring sessionNone (such as cmdGame) do not require an active
	// access token to inspect public products.
	if inv.session != sessionNone {
		if err := d.Init(ctx); err != nil {
			return reportError(stderr, err)
		}
	}

	if inv.cfg.MsgLevel >= msgLevelVerbose {
		fmt.Fprintf(stderr, "verbose: executing command\n")
	}

	switch inv.cmd {
	case cmdAuthLogin:
		// The login already happened: the tree declares sessionExplicitLogin,
		// sessionRequest turned that into AllowLogin, and OpenWith ran the flow
		// before this switch was reached, so the command's work is done.
		return outcomeOK

	case cmdGame:
		info, err := d.GetProductInfo(ctx, inv.target.Product, productRefMode(inv))
		if err != nil {
			return reportError(stderr, err)
		}
		if inv.json {
			if err := util.WriteStyledJSON(stdout, info); err != nil {
				return reportError(stderr, err)
			}
			return outcomeOK
		}
		renderProductInfo(stdout, info)
		return outcomeOK

	case cmdInstallOptions:
		platform := core.PlatformName(inv.cfg.DownloadConfig.GalaxyPlatform)
		res, err := d.InstallOptions(ctx, inv.target.Product, productRefMode(inv), platform, inv.target.Build)
		if err != nil {
			return reportError(stderr, err)
		}
		if inv.json {
			if err := util.WriteStyledJSON(stdout, res); err != nil {
				return reportError(stderr, err)
			}
			return outcomeOK
		}
		if err := renderInstallOptions(stdout, res); err != nil {
			return reportError(stderr, err)
		}
		return outcomeOK

	case cmdListGames, cmdListTags, cmdListWishlist:
		if err := renderList(ctx, d, inv, stdout); err != nil {
			return reportError(stderr, err)
		}
		return outcomeOK

	case cmdBackupList:
		return runListDetails(ctx, d, inv, stdout, stderr)

	case cmdGalaxyBuilds, cmdGalaxyManifest:
		// "galaxy builds" lists a product's builds; "galaxy manifest" shows one
		// build's manifest. They are separate commands, so a manifest request
		// without a build means the build a plain install would pick (index 0),
		// not "list them".
		build := inv.target.Build
		if inv.cmd == cmdGalaxyManifest && build == "" {
			build = "0"
		}
		res, err := d.ShowBuilds(ctx, inv.target.Product, build, productRefMode(inv))
		if inv.json {
			// Under JSON mode, human notices go to stderr to guarantee pure stdout JSON.
			if res.Notice.Text != "" {
				if res.Notice.Err {
					fmt.Fprintln(stderr, res.Notice.Text)
				} else {
					fmt.Fprintln(stderr, res.Notice.Text)
				}
			}
		} else {
			// The Linux support messages are rendered even when the run then fails:
			// they go to stdout and the missing fallback is reported on stderr
			// afterwards.
			renderNotice(stdout, stderr, res.Notice)
		}
		if err != nil {
			return reportError(stderr, err)
		}
		if res.Manifest != nil {
			if err := renderManifest(stdout, res.Manifest); err != nil {
				return reportError(stderr, err)
			}
			return outcomeOK
		}
		if inv.cmd == cmdGalaxyManifest {
			// Manifest Notice-only success produces a null JSON payload.
			if inv.json {
				if err := util.WriteStyledJSON(stdout, nil); err != nil {
					return reportError(stderr, err)
				}
				return outcomeOK
			}
			return outcomeOK
		}
		if inv.json {
			builds := res.Builds
			if builds == nil {
				builds = []core.BuildRow{}
			}
			if err := util.WriteStyledJSON(stdout, builds); err != nil {
				return reportError(stderr, err)
			}
			return outcomeOK
		}
		if err := renderBuilds(stdout, res.Builds); err != nil {
			return reportError(stderr, err)
		}
		return outcomeOK

	case cmdGalaxyCDNs:
		res, err := d.ListCDNs(ctx, inv.target.Product, inv.target.Build, productRefMode(inv))
		if inv.json {
			if res.Notice.Text != "" {
				fmt.Fprintln(stderr, res.Notice.Text)
			}
		} else {
			renderNotice(stdout, stderr, res.Notice)
		}
		if err != nil {
			return reportError(stderr, err)
		}
		if inv.json {
			names := res.Names
			if names == nil {
				names = []string{}
			}
			if err := util.WriteStyledJSON(stdout, names); err != nil {
				return reportError(stderr, err)
			}
			return outcomeOK
		}
		if err := renderCDNNames(stdout, res.Names); err != nil {
			return reportError(stderr, err)
		}
		return outcomeOK

	case cmdVerify:
		// A verification reads the same installation an install writes, so it
		// resolves its target exactly like one — but only observes: the plan is
		// built in verify mode (no destructive work, no free-space answer) and
		// every expected file is classified.
		req := core.NewInstallRequest(inv.cfg, inv.target.Product, inv.target.Build, productRefMode(inv))
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

	case cmdBackupDownload:
		if len(inv.args) == 1 && strings.Contains(inv.args[0], "/") {
			return ui.runWebsiteFiles(ctx, d, inv, stdout, stderr, progress)
		}
		if len(inv.args) >= 2 {
			filesInv := inv
			game := inv.args[0]
			specs := make([]string, 0, len(inv.args)-1)
			for _, fileID := range inv.args[1:] {
				specs = append(specs, game+"/"+fileID)
			}
			filesInv.args = specs
			return ui.runWebsiteFiles(ctx, d, filesInv, stdout, stderr, progress)
		}
		return ui.runWebsiteDownload(ctx, d, inv, stdout, stderr, progress)

	case cmdOrphansRemove:
		return ui.runOrphansRemove(ctx, d, inv, stdout, stderr)

	case cmdInstall:
		// The lifecycle has one owner and one order: signal context → renderer
		// start → the install with Stop deferred → signal restore. Stop covers
		// every exit path — error, cancellation and panic — but never swallows:
		// a panic propagates after the cleanup, as Go would.
		req := core.NewInstallRequest(inv.cfg, inv.target.Product, inv.target.Build, productRefMode(inv))
		return stopOutcome(ui.runInstall(ctx, d, req, inv.cfg, progress))
	}

	// Unreachable while the command tree and this switch agree; keeping it an
	// operational failure means a future command that forgets a case fails
	// loudly instead of exiting 0.
	return reportError(stderr, fmt.Errorf("%s has no handler", inv.cmd.path()))
}

// productRefMode maps --regex onto the mode the selector reads. The default is
// the product's own name, which is what `list games` prints.
func productRefMode(inv invocation) core.ProductRefMode {
	if inv.productRefRegex {
		return core.ProductRefRegex
	}
	return core.ProductRefExact
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
// finalizes with the right state: the result variable starts at failed, so an
// unwound panic finalizes as failed and then propagates — Stop cleans up,
// nothing is swallowed.
func (c *console) runInstall(ctx context.Context, d *core.Downloader, req core.InstallRequest, cfg config.Config, progress *transfer.Progress) stopReason {
	ctx, stopSignal := signal.NotifyContext(ctx, os.Interrupt)
	c.attachInstallUI(cfg, progress, "Installation")

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
	// instead of being swallowed by the context.
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
