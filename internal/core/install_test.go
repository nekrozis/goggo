package core

import (
	"context"
	"errors"
	"testing"
)

// TestInstallNotImplemented locks the producer this step exists to create: the
// command is recognised and reaches the orchestration layer, where it stops
// loudly instead of reporting a success that never happened.
//
// The message is the one the front end prints after "Error: ", so it is
// asserted verbatim; the sentinel is asserted separately, because the front end
// must be able to recognise the failure without matching text.
func TestInstallNotImplemented(t *testing.T) {
	var d Downloader
	err := d.Install(context.Background(), InstallRequest{ProductID: "1495134320"})

	if !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("err = %v, want it to match ErrNotImplemented", err)
	}
	if want := "galaxy install: not implemented in this build"; err.Error() != want {
		t.Errorf("err = %q, want %q", err.Error(), want)
	}
}
