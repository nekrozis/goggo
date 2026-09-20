package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"

	"github.com/nekrozis/goggo/internal/core"
)

// runOrphansCheck lists the files the installation does not account for.
//
// Finding them is not a failure, however many there are: a save file or a mod
// under the installation root is not explained by the manifest. The exit code
// therefore reports whether the CHECK worked — unlike `verify`, whose mismatch
// means the installation does not match what it should be.
func (c *console) runOrphansCheck(ctx context.Context, d *core.Downloader, inv invocation, stdout, stderr io.Writer) outcome {
	res, err := d.CheckOrphans(ctx, core.NewInstallRequest(inv.cfg, inv.target.Product, inv.target.Build))
	renderNotices(stdout, stderr, res.Notices)
	if err != nil {
		return reportError(stderr, err)
	}
	renderOrphans(stdout, res)
	return outcomeOK
}

// runOrphansRemove walks the installation, shows the list, obtains the
// authorization for exactly that list and removes it.
//
// The authorization is the front end's business (the terminal, `--yes` and the
// answer), while core's is deleting the paths it was handed. The list is printed
// before the question, so what the user approves is what they just read (D10).
//
// The interrupt context is set up here because a cancelled removal stops and
// still reports what it deleted (D33, D20).
func (c *console) runOrphansRemove(ctx context.Context, d *core.Downloader, inv invocation, stdout, stderr io.Writer) outcome {
	ctx, stopSignal := signal.NotifyContext(ctx, os.Interrupt)
	defer stopSignal()

	res, err := d.CheckOrphans(ctx, core.NewInstallRequest(inv.cfg, inv.target.Product, inv.target.Build))
	renderNotices(stdout, stderr, res.Notices)
	if err != nil {
		return reportError(stderr, err)
	}
	renderOrphans(stdout, res)
	return c.removeOrphans(ctx, d, res, inv.yes, stdout, stderr)
}

// removeOrphans authorizes and applies one removal.
//
// The list has already been shown, so what the user approves is what they just
// read (D10), and the authorization has exactly two sources: the explicit --yes
// flag, which says "I authorized this — do not ask" (D16), or the answer the
// terminal gives. A run without authorization deletes nothing and reports that.
func (c *console) removeOrphans(ctx context.Context, d *core.Downloader, res core.OrphansResult, yes bool, stdout, stderr io.Writer) outcome {
	if len(res.Files) == 0 {
		// Nothing to authorize and nothing to delete; the count line above
		// already said so.
		return outcomeOK
	}
	if !c.authorized(yes, len(res.Files)) {
		fmt.Fprintln(stderr, "Nothing was deleted.")
		return outcomeOK
	}

	fmt.Fprintf(stdout, "Deleting %d orphaned files\n", len(res.Files))
	attempts, err := d.RemoveOrphans(ctx, res)
	failed := 0
	for _, attempt := range attempts {
		if attempt.Err != nil {
			failed++
			// The CLI names the reason as well as the object: a failed deletion
			// is the one thing here a user may have to act on with the
			// filesystem (the install tail reports the object alone).
			fmt.Fprintf(stderr, "Failed to delete %s: %v\n", attempt.Path, attempt.Err)
			continue
		}
		fmt.Fprintf(stdout, "  %s\n", res.Relative(attempt.Path))
	}
	if err != nil {
		// An interruption: what was deleted has been reported above, and the
		// run says why it stopped.
		if errors.Is(err, context.Canceled) || ctx.Err() != nil {
			fmt.Fprintf(stderr, "Error: %v\n", err)
			return outcomeInterrupted
		}
		return reportError(stderr, err)
	}
	if failed > 0 {
		return outcomeOperationFailure
	}
	return outcomeOK
}

// authorized reports whether a removal may go ahead.
//
// An explicit --yes IS the authorization — asking anyway would make the flag
// meaningless on a terminal, which is where a destructive command is normally
// run (D16). Without it the terminal is asked, and anything short of a clear yes
// is a no.
func (c *console) authorized(yes bool, count int) bool {
	if yes {
		return true
	}
	return c.confirm(deleteQuestion(count))
}

// renderOrphans writes the walk's answer: the shared context (the root) once, one
// line per orphan relative to it, and the count. The relative form is what makes
// a long list readable, and it is unambiguous because the header named the root.
func renderOrphans(w io.Writer, res core.OrphansResult) {
	if res.InstallPath != "" {
		fmt.Fprintf(w, "Checking → %s\n", res.InstallPath)
	}
	for _, path := range res.Files {
		fmt.Fprintf(w, "  %s\n", res.Relative(path))
	}
	if len(res.Files) == 0 {
		fmt.Fprintln(w, "No orphaned files")
		return
	}
	fmt.Fprintf(w, "%d orphaned files\n", len(res.Files))
}

// deleteQuestion is the authorization a destructive run asks for. The default is
// No, and the wording counts what is about to go.
func deleteQuestion(n int) string {
	if n == 1 {
		return "Delete this file? [y/N] "
	}
	return fmt.Sprintf("Delete these %d files? [y/N] ", n)
}
