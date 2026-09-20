package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/nekrozis/goggo/internal/core"
	"github.com/nekrozis/goggo/internal/reconcile"
)

// verifyCounts is the tally a verification reports: one bucket per observed
// fact, plus the destinations whose state could not be observed at all.
//
// The failure bucket is NOT a fifth status — there is no fact about a file that
// could not be read, and the per-object errors go to stderr. It is counted so
// the summary's total stays honest.
type verifyCounts struct {
	ok, nd, md5, fs, unreadable int
}

// total is every destination the verification covered, whatever its state.
func (c verifyCounts) total() int { return c.ok + c.nd + c.md5 + c.fs + c.unreadable }

// problems is how many destinations need attention.
func (c verifyCounts) problems() int { return c.total() - c.ok }

// parts lists the non-zero counts in a fixed order: the routine state first,
// then the three abnormal facts, then the observation failures.
func (c verifyCounts) parts() []string {
	var parts []string
	add := func(n int, label string) {
		if n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, label))
		}
	}
	add(c.ok, "OK")
	add(c.nd, "not downloaded")
	add(c.md5, "md5 mismatch")
	add(c.fs, "size mismatch")
	add(c.unreadable, "unreadable")
	return parts
}

// renderVerify writes one verification report and returns the run's outcome.
//
// The installation root is printed once, the abnormal objects one line each with
// their absolute path, and the routine state as a count. The arithmetic of that
// line is what makes "no per-file line" unambiguous: every file is either
// counted or named.
//
// The exit code carries the answer: a verification is meant to be usable from a
// script.
func renderVerify(out, errOut io.Writer, res core.VerifyResult) outcome {
	if res.InstallPath != "" {
		fmt.Fprintf(out, "Verifying → %s\n", res.InstallPath)
	}

	counts := verifyCounts{}
	for _, fact := range res.Facts {
		if fact.Err != nil {
			counts.unreadable++
			// The diagnostic names the file and keeps its absolute path: it is
			// the one row a user has to act on with the filesystem itself.
			fmt.Fprintf(errOut, "%v\n", fact.Err)
			continue
		}
		switch fact.Status {
		case reconcile.StatusOK:
			counts.ok++
		case reconcile.StatusND:
			counts.nd++
		case reconcile.StatusMD5:
			counts.md5++
		case reconcile.StatusFS:
			counts.fs++
		default:
			// A fact nobody classified must not read as healthy: it takes the
			// attention bucket (the status zero value is deliberately not OK).
			counts.unreadable++
		}
		if fact.Status != reconcile.StatusOK {
			fmt.Fprintf(out, "%-3v  %s\n", fact.Status, fact.Destination)
		}
	}

	if counts.total() == 0 {
		// Nothing to verify is a state worth stating, the way the install's
		// zero-transfer path states its own.
		fmt.Fprintln(out, "Nothing to verify.")
	} else {
		fmt.Fprintf(out, "%d files: %s\n", counts.total(), strings.Join(counts.parts(), ", "))
	}

	if counts.problems() > 0 {
		return outcomeOperationFailure
	}
	return outcomeOK
}
