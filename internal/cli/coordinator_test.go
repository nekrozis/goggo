package cli

import (
	"strconv"
	"strings"
	"testing"
)

// cursorUpRows reports how far up the escape sequences in s move the cursor.
// The coordinator's contract is the row arithmetic — the cursor comes back to
// the top of the block it painted — not the spelling of the sequence, so the
// tests below measure the distance travelled and tolerate any equivalent
// encoding ("\033[3A" or "\033[2A\033[1A").
func cursorUpRows(s string) int {
	total := 0
	for i := 0; i < len(s); i++ {
		if s[i] != '\033' || i+1 >= len(s) || s[i+1] != '[' {
			continue
		}
		j := i + 2
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
		}
		if j >= len(s) || s[j] != 'A' {
			continue
		}
		rows, err := strconv.Atoi(s[i+2 : j])
		if err != nil {
			rows = 1 // ESC[A is the one-row form
		}
		total += rows
		i = j
	}
	return total
}

// eraseToEndAt returns the index of the escape sequence that clears from the
// cursor to the end of the screen, or -1 if s has none: the frame that was on
// screen is gone, whatever the sequence's spelling.
func eraseToEndAt(s string) int {
	for i := 0; i+1 < len(s); i++ {
		if s[i] != '\033' || s[i+1] != '[' {
			continue
		}
		j := i + 2
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
		}
		if j < len(s) && s[j] == 'J' {
			return i
		}
	}
	return -1
}

// TestCoordinatorFrameTransaction locks the Frame transaction's shape: the
// first draw paints the rows with no cursor work at all; the second takes the
// cursor back to the top of the block it painted — exactly the previous frame's
// rows, which the no-wrap layout invariant makes trustworthy — and erases it
// before painting the new rows.
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
	if got := cursorUpRows(second); got != 3 {
		t.Errorf("second frame = %q, want the cursor returned to the start of the 3 rows above, got %d", second, got)
	}
	// The erase happens before the new rows: the frame is replaced, not
	// appended to.
	eraseAt, rowAt := eraseToEndAt(second), strings.Index(second, "alpha")
	if eraseAt < 0 {
		t.Errorf("second frame = %q, want the previous frame erased", second)
	} else if rowAt < 0 || eraseAt > rowAt {
		t.Errorf("second frame = %q, want the old rows erased before the new ones are painted", second)
	}
	if !strings.Contains(second, "alpha\n") {
		t.Errorf("second frame = %q, want the new rows", second)
	}
}

// TestCoordinatorDiagnosticTransaction locks the diagnostic transaction: a
// diagnostic lands on stderr with the frame torn down around it, the frame is
// redrawn intact, and the NEXT frame starts from the same canonical position —
// the same row count is erased as was on screen.
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
	if got := cursorUpRows(sequence); got != 3 {
		t.Errorf("stdout = %q, want the diagnostic transaction to return the cursor to the 3 rows above, got %d", sequence, got)
	}
	if eraseToEndAt(sequence) < 0 {
		t.Errorf("stdout = %q, want the frame erased around the diagnostic", sequence)
	}
	// The frame comes back exactly as it was: all three rows, in order, after
	// the diagnostic transaction's cursor work.
	redrawn := sequence
	for _, row := range []string{"one\n", "two\n", "three\n"} {
		at := strings.Index(redrawn, row)
		if at < 0 {
			t.Fatalf("stdout = %q, want %q redrawn", sequence, row)
		}
		redrawn = redrawn[at+len(row):]
	}

	// The next frame erases the same 3 rows: the redraw after the diagnostic
	// restored the canonical position, not one row more or fewer.
	out.Reset()
	errOut.Reset()
	c.drawFrame([]string{"alpha"})
	if got := cursorUpRows(out.String()); got != 3 {
		t.Errorf("next frame = %q, want the same 3-row return (cursor ownership held), got %d", out.String(), got)
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
	if got := cursorUpRows(sequence); got != 2 {
		t.Errorf("stdout = %q, want the frame's 2 rows taken down for the notice, got %d", sequence, got)
	}
	if eraseToEndAt(sequence) < 0 {
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

// TestConsoleRoutesThroughCoordinator locks the single-entry wiring: during the
// install's TTY lifetime the console's Out/ErrOut return coordinator writers — a
// caller that only knows io.Writer still lands on the coordinator; after the
// scope ends the plain streams come back.
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
	// diagnostic itself lands on stderr with no cursor work of its own. The
	// frame on screen was one row, so the cursor travels up one row twice — the
	// two transactions.
	if got := cursorUpRows(sequence); got != 2 {
		t.Errorf("stdout = %q, want both notices to take the one-row frame down and back, got %d", sequence, got)
	}
	if !strings.Contains(sequence, "core notice on stdout") {
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
