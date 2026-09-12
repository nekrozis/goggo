package cli

import (
	"strings"
	"testing"
)

// TestCoordinatorFrameTransaction locks the Frame transaction's shape: the
// first draw paints the rows; the second erases exactly the previous frame's
// rows before painting — the row arithmetic the no-wrap layout invariant
// makes trustworthy (review UI1 v3 §6.C).
func TestCoordinatorFrameTransaction(t *testing.T) {
	var out, errOut strings.Builder
	c := newTerminalCoordinator(&out, &errOut)

	c.drawFrame([]string{"one", "two", "three"})
	first := out.String()
	if !strings.Contains(first, "one\n") || strings.Contains(first, "\x1b[") {
		t.Fatalf("first frame = %q, want plain rows without cursor work", first)
	}

	out.Reset()
	c.drawFrame([]string{"alpha", "beta"})
	second := out.String()
	if !strings.HasPrefix(second, "\033[3A\r\033[J") {
		t.Errorf("second frame = %q, want an erase of exactly the 3 previous rows", second)
	}
	if !strings.Contains(second, "alpha\n") {
		t.Errorf("second frame = %q, want the new rows", second)
	}
}

// TestCoordinatorDiagnosticTransaction is the P0 regression (review UI1 v3,
// constraint 8): a diagnostic lands on stderr with the frame torn down around
// it, the frame is redrawn intact, and the NEXT frame starts from the same
// canonical position — the same row count is erased as was on screen.
func TestCoordinatorDiagnosticTransaction(t *testing.T) {
	var out, errOut strings.Builder
	c := newTerminalCoordinator(&out, &errOut)
	c.drawFrame([]string{"one", "two", "three"})

	out.Reset()
	errOut.Reset()
	c.writeErr("Warning: something happened")

	if got := errOut.String(); got != "Warning: something happened\n" {
		t.Errorf("stderr = %q, want exactly the diagnostic", got)
	}
	sequence := out.String()
	erase := "\033[3A\r\033[J"
	if !strings.HasPrefix(sequence, erase) {
		t.Errorf("stdout = %q, want the diagnostic transaction to start with the frame erase", sequence)
	}
	if !strings.HasSuffix(sequence, "three\n") {
		t.Errorf("stdout = %q, want the frame redrawn after the diagnostic", sequence)
	}

	// The next frame erases the same 3 rows: the redraw after the diagnostic
	// restored the canonical position, not one row more or fewer.
	out.Reset()
	errOut.Reset()
	c.drawFrame([]string{"alpha"})
	if !strings.HasPrefix(out.String(), erase) {
		t.Errorf("next frame = %q, want the same 3-row erase (cursor ownership held)", out.String())
	}
	if errOut.Len() != 0 {
		t.Errorf("stderr grew to %q, want the diagnostic to have stayed put", errOut.String())
	}
}

// TestCoordinatorOutTransaction locks the console routing: a plain writer
// handed out by the console lands its line as one transaction, frame down and
// frame up — a core notice can never interleave with the live frame.
func TestCoordinatorOutTransaction(t *testing.T) {
	var out, errOut strings.Builder
	c := newTerminalCoordinator(&out, &errOut)
	c.drawFrame([]string{"one", "two"})

	out.Reset()
	if _, err := c.writer(false).Write([]byte("Deleting old/build.dat\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	sequence := out.String()
	if !strings.HasPrefix(sequence, "\033[2A\r\033[J") {
		t.Errorf("stdout = %q, want the frame erased first", sequence)
	}
	if !strings.Contains(sequence, "Deleting old/build.dat\n") {
		t.Errorf("stdout = %q, want the notice line", sequence)
	}
	if !strings.HasSuffix(sequence, "two\n") {
		t.Errorf("stdout = %q, want the frame restored", sequence)
	}
}

// TestCoordinatorFinalizeRefusesWrites locks the Stop transaction: the final
// state is written once and every later write — frame, diagnostic, plain — is
// refused, so nothing can smear output after the run's closing state.
func TestCoordinatorFinalizeRefusesWrites(t *testing.T) {
	var out, errOut strings.Builder
	c := newTerminalCoordinator(&out, &errOut)
	c.drawFrame([]string{"one"})

	out.Reset()
	c.finalize([]string{"3 files completed"})
	final := out.String()
	if !strings.Contains(final, "3 files completed") {
		t.Errorf("finalize output = %q, want the terminal state", final)
	}

	out.Reset()
	errOut.Reset()
	c.drawFrame([]string{"stale"})
	c.writeErr("late warning")
	c.writer(false).Write([]byte("late line\n"))
	if out.Len() != 0 || errOut.Len() != 0 {
		t.Errorf("writes after finalize: out=%q err=%q, want silence", out.String(), errOut.String())
	}
}

// TestConsoleRoutesThroughCoordinator locks the single-entry wiring (review
// UI1 v3 §6.C, constraint 1): during the install's TTY lifetime the console's
// Out/ErrOut return coordinator writers — a caller that only knows io.Writer
// still lands on the coordinator; after the scope ends the plain streams come
// back.
func TestConsoleRoutesThroughCoordinator(t *testing.T) {
	var out, errOut strings.Builder
	c := newConsole(strings.NewReader(""), &out, &errOut)

	c.coord = newTerminalCoordinator(&out, &errOut)
	c.coord.drawFrame([]string{"frame-row"})
	out.Reset()
	errOut.Reset()

	fmtFprintln := func(w interface{ Write([]byte) (int, error) }, text string) {
		w.Write([]byte(text + "\n"))
	}
	fmtFprintln(c.Out(), "core notice on stdout")
	fmtFprintln(c.ErrOut(), "core notice on stderr")
	sequence, diag := out.String(), errOut.String()
	// The frame erase always writes to stdout (the frame's stream); the
	// diagnostic itself lands on stderr with no cursor work of its own.
	if !strings.Contains(sequence, "\033[1A\r\033[J") || !strings.Contains(sequence, "core notice on stdout") {
		t.Errorf("stdout = %q, want notice routed as a transaction", sequence)
	}
	if !strings.Contains(sequence, "frame-row\n") {
		t.Errorf("stdout = %q, want the frame redrawn after the notice", sequence)
	}
	if diag != "core notice on stderr\n" {
		t.Errorf("stderr = %q, want the bare diagnostic line", diag)
	}

	// Outside the install lifetime the plain streams come back.
	c.endInstallScope()
	out.Reset()
	fmtFprintln(c.Out(), "plain again")
	if strings.Contains(out.String(), "\033[") {
		t.Errorf("stdout = %q, want no coordinator after the scope ended", out.String())
	}
}
