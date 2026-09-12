package core

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nekrozis/goggo/internal/reconcile"
	"github.com/nekrozis/goggo/internal/transfer"
)

// ErrNotImplemented reports a command this build recognises but has not
// implemented yet. It is the orchestration-layer counterpart of the front end's
// "recognised but not implemented" answer: the same notice, raised where the
// command would have run instead of where the option is parsed. The Linux
// installer fallback (plan.go) is its current producer.
var ErrNotImplemented = errors.New("not implemented in this build")

// Install resolves one install request into a plan, applies its destructive
// changes, runs the transfer and unpacks the small-files containers and the
// orphan check. Display output — the plan summary, the deletions, the transfer
// messages — streams through the front end's console as it happens.
//
// Task failures during the transfer leave as error events and do not fail the
// run: the C++ source never folds them into the exit code either
// (downloader.cpp:886 calls a void function; review D65a). The post-transfer
// steps run only when the transfer itself returned without a cancelled
// context, mirroring the C++ sequence after the thread join.
func (d *Downloader) Install(ctx context.Context, req InstallRequest) error {
	res, err := d.BuildPlan(ctx, req)
	d.emitNotices(res.Messages)
	if err != nil {
		return err
	}

	failures, err := d.ApplyPlanChanges(ctx, res.Plan)
	d.emitNotices(failures)
	if err != nil {
		return err
	}

	if err := transfer.Run(ctx, res.Plan.Tasks, d.transferOptions(), transfer.RunDeps{
		HTTP:     d.http,
		URL:      d.chunkURLProvider(),
		Observer: d.transferObserver(),
		Progress: d.progress,
	}); err != nil {
		return err
	}

	// The skipped set was a planning-time observation, so the install closes
	// the window that observation opened: every destination the plan marked
	// "already up to date" is re-checked here, after the transfer finished
	// and before any post-transfer step consumes the installation (review
	// RES1 v2 §2, RES1-R1). A file that changed meanwhile fails the install —
	// no task is recreated, nothing is re-downloaded, no file is touched;
	// rerunning the install reconciles it.
	if err := revalidateSkipped(res.Skipped); err != nil {
		return err
	}

	// The post-transfer steps (downloader.cpp:4265-4343): the small-files
	// containers unpack and the orphan check. Both print as they go and both
	// are non-fatal in their per-item failures, the way the C++ source is.
	if err := d.ExtractSmallFilesContainers(ctx, res); err != nil {
		return err
	}
	return d.CheckOrphanedFiles(ctx, res)
}

// revalidateSkipped re-observes the plan's skipped destinations and fails on
// the first one that no longer satisfies its item. An observation failure
// (an unreadable file) is an installation error too: the install must not
// report success over a state it could not verify (decisions D43, RES1-R1).
func revalidateSkipped(skipped []SkippedFile) error {
	for _, sf := range skipped {
		complete, err := reconcile.IsComplete(sf.Item, sf.Destination)
		if err != nil {
			return fmt.Errorf("Failed to inspect %s: %w", sf.Destination, err)
		}
		if !complete {
			return fmt.Errorf("%s changed during installation; run the install again", sf.Destination)
		}
	}
	return nil
}

// transferObserver picks the observer for a transfer run. A front end that can
// consume the whole event stream — the CLI renderer — gets it through the
// optional-ability assertion; a plain Console keeps the message-only adapter
// (review D75). The capability interface stays unexported; the CLI satisfies
// it structurally.
func (d *Downloader) transferObserver() transfer.Observer {
	if sink, ok := d.ui.(transferEventSink); ok {
		return sink
	}
	return observerFunc(d.onTransferEvent)
}

// emitNotices renders plan-phase messages through the front end's console:
// error notices go to the error stream, everything else to the output stream.
func (d *Downloader) emitNotices(notices []Notice) {
	for _, n := range notices {
		if n.Err {
			fmt.Fprintln(d.ui.ErrOut(), n.Text)
		} else {
			fmt.Fprintln(d.ui.Out(), n.Text)
		}
	}
}

// onTransferEvent adapts transfer events to the front end's console. Every
// message kind lands on the output stream, the way the C++ message queue does
// (downloader.cpp:3538-3541 renders the whole queue via std::cout); the two
// task-transition kinds and per-chunk progress are rendered by the progress
// step (S20) and are ignored here for now.
func (d *Downloader) onTransferEvent(ev transfer.Event) {
	switch ev.Kind {
	case transfer.EventMessageInfo, transfer.EventMessageWarning,
		transfer.EventMessageError, transfer.EventMessageSuccess:
		fmt.Fprintln(d.ui.Out(), ev.Text)
	}
}

// transferOptions maps the configuration onto the transfer tuning. Both
// durations convert from milliseconds, the upstream unit of --wait and
// --progress-interval (main.cpp:292,313).
func (d *Downloader) transferOptions() transfer.Options {
	return transfer.Options{
		Workers:          int(d.cfg.Threads),
		Retries:          d.cfg.Retries,
		Wait:             time.Duration(d.cfg.Wait) * time.Millisecond,
		ProgressInterval: time.Duration(d.cfg.ProgressInterval) * time.Millisecond,
	}
}

// chunkURLProvider builds the transfer's URL seam: the Galaxy resolution with
// this run's CDN priority, refreshing the token through this downloader's
// credentials.
func (d *Downloader) chunkURLProvider() *chunkURLProvider {
	return &chunkURLProvider{
		galaxy:   d.galaxy,
		priority: d.cfg.DownloadConfig.GalaxyCDNPriority,
		refresh:  d.refreshAndSave,
		expired:  func() bool { return d.token.IsExpired() },
	}
}

// observerFunc adapts a function into transfer.Observer.
type observerFunc func(transfer.Event)

// OnEvent implements transfer.Observer.
func (f observerFunc) OnEvent(ev transfer.Event) { f(ev) }
