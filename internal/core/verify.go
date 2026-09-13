package core

import (
	"context"
	"fmt"

	"github.com/nekrozis/goggo/internal/model"
	"github.com/nekrozis/goggo/internal/reconcile"
)

// FileFact is what a verification observed about one expected file: the fact
// plus the item it was compared against, so a front end can report the size and
// the hash without reading anything itself.
type FileFact struct {
	Destination string
	Item        model.GalaxyDepotItem

	// Status is the observed fact. It is StatusUnset when Err is set: there is
	// no fact about a file that could not be read, and neither "absent" nor
	// "fine" may be claimed for it (decisions D43).
	Status reconcile.FileStatus

	// Err is the observation failure — the file's state could not be read.
	Err error
}

// VerifyResult is one verification run's outcome. Like PlanResult it is data:
// the front end renders it.
type VerifyResult struct {
	// InstallPath is the installation root the plan was built against, empty
	// when the plan stopped before it was resolved (a generation gate, or the
	// Linux fallback).
	InstallPath string

	// Facts covers the plan's whole expected file set — one entry per file, in
	// the plan's order (path order), with failures keeping their place. The
	// order is a contract of this type, not of the renderer (review S5).
	Facts []FileFact

	// Notices are the plan-phase messages a verification should show: the
	// blacklist report, the verbose item lines, the Linux fallback. The
	// install-shaped summary is not among them (planMode, review S5).
	Notices []Notice
}

// Verify reports the fact for every file the installation is expected to have.
//
// It is read-only in the strictest sense (review CLI1 §7, §13②③): it builds the
// plan in verify mode, which neither downloads nor deletes nor answers for disk
// space, and then only observes — no file is written, replaced, removed or
// touched, and a mismatch is never repaired. The file set it reports on is
// PlanResult.Expected alone, so a verification and an install cannot disagree
// about which files the installation owns (review S5).
//
// An observation failure does not stop the run: one unreadable file must not
// hide the state of the rest, so it is reported as a fact with an error and the
// walk continues.
func (d *Downloader) Verify(ctx context.Context, req InstallRequest) (VerifyResult, error) {
	res, err := d.buildPlan(ctx, req, planForReadOnly)
	out := VerifyResult{InstallPath: res.InstallPath, Notices: res.Messages}
	if err != nil {
		return out, err
	}

	for _, expected := range res.Expected {
		status, err := reconcile.ClassifyExistingFile(expected.Item, expected.Destination)
		if err != nil {
			out.Facts = append(out.Facts, FileFact{
				Destination: expected.Destination,
				Item:        expected.Item,
				Err:         fmt.Errorf("%s: %w", expected.Destination, err),
			})
			continue
		}
		out.Facts = append(out.Facts, FileFact{
			Destination: expected.Destination,
			Item:        expected.Item,
			Status:      status,
		})
	}
	return out, nil
}
