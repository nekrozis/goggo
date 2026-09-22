package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"

	"github.com/nekrozis/goggo/internal/core"
	"github.com/nekrozis/goggo/internal/transfer"
	"github.com/nekrozis/goggo/internal/util"
)

// This file maps the two download commands onto the install lifecycle: signal
// context around the run, renderer frame around the transfer, one
// classification of the terminal state. Core returns structured results and
// never writes to a stream; every line here is the front end rendering its own
// command.

// runWebsiteDownload drives "download <game>...". The aggregate exit contract:
// every task ran or was authorised-skipped ⇒ 0; any operational failure —
// visible as events during the run and enumerated after the frame — ⇒ 1;
// cancellation ⇒ 130; a core error before or instead of the queue ⇒ 1.
func (c *console) runWebsiteDownload(ctx context.Context, d *core.Downloader, inv invocation, stdout, stderr io.Writer, progress *transfer.Progress) outcome {
	ctx, stopSignal := signal.NotifyContext(ctx, os.Interrupt)
	c.attachInstallUI(inv.cfg, progress, "Download")

	var (
		res    core.WebsiteDownloadResult
		runErr error
	)
	result := stopFailed
	c.renderer.Start()
	func() {
		defer func() { c.renderer.Stop(result) }()
		res, runErr = d.DownloadWebsite(ctx, core.WebsiteDownloadRequest{Products: inv.args, RefMode: productRefMode(inv)})
		result = classifyInstallResult(runErr, ctx)
	}()
	stopSignal()
	c.endInstallScope()

	renderNotices(stdout, stderr, res.Notices)
	renderArtifacts(stdout, stderr, res.Saved)
	// The "Total size" line prints whenever a queue existed — including the run
	// the free-space gate then refused. It is rendered after the frame, not
	// before the queue starts, because the renderer owns the terminal while it
	// lives. The number is core's, the placement is the front end's.
	if res.Tasks > 0 {
		fmt.Fprintf(stdout, "Total size: %s\n", util.SizeString(uint64(res.TotalSize), inv.cfg.UnitFormat))
	}

	if result == stopCanceled {
		return outcomeInterrupted
	}
	if runErr != nil {
		fmt.Fprintf(stderr, "Error: %v\n", runErr)
		return outcomeOperationFailure
	}
	for _, f := range res.Failures {
		fmt.Fprintf(stderr, "Failed: %s: %v\n", f.Destination, f.Err)
	}
	for _, s := range res.Skipped {
		switch s.Evidence {
		case transfer.SkipVerifiedManifest:
			fmt.Fprintf(stdout, "Skipped (verified via local xml manifest): %s\n", s.Destination)
		default:
			fmt.Fprintf(stdout, "Skipped (size match, no xml manifest, chunk integrity unverified): %s\n", s.Destination)
		}
	}
	if res.Failed() {
		return outcomeOperationFailure
	}
	return outcomeOK
}

// runWebsiteFiles drives "download file <spec>...". Every spec runs to the end
// whatever its siblings do; a successful download is kept even when another
// spec fails; the command exits 1 iff any spec failed. Cancellation
// outranks the aggregate: a stopped run reports 130, not the per-spec noise of
// the specs it never reached.
func (c *console) runWebsiteFiles(ctx context.Context, d *core.Downloader, inv invocation, stdout, stderr io.Writer, progress *transfer.Progress) outcome {
	// -o must not name an existing directory: that is a wrong argument, so it
	// is answered as a usage error before any network work.
	if inv.outputFile != "" {
		if fi, err := os.Stat(inv.outputFile); err == nil && fi.IsDir() {
			return reportError(stderr, usagef("-o names a directory: %s", inv.outputFile))
		}
	}

	ctx, stopSignal := signal.NotifyContext(ctx, os.Interrupt)
	c.attachInstallUI(inv.cfg, progress, "Download")

	var res core.WebsiteFileResult
	result := stopFailed
	c.renderer.Start()
	func() {
		defer func() { c.renderer.Stop(result) }()
		res = d.DownloadWebsiteFiles(ctx, inv.args, inv.outputFile, productRefMode(inv))
		switch {
		case ctx.Err() != nil:
			result = stopCanceled
		case res.Failed():
			result = stopFailed
		default:
			result = stopCompleted
		}
	}()
	stopSignal()
	c.endInstallScope()

	if result == stopCanceled {
		return outcomeInterrupted
	}
	failed := false
	for _, o := range res.Outcomes {
		if o.Err != nil {
			failed = true
			fmt.Fprintf(stderr, "Failed: %s: %v\n", o.Spec, o.Err)
		}
	}
	if failed {
		return outcomeOperationFailure
	}
	return outcomeOK
}
