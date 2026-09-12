package transfer

import (
	"github.com/nekrozis/goggo/internal/httpx"
)

// RunDeps is the construction-time dependency bundle the run loop takes: the
// HTTP exit, the URL seam, the event sink and the optional progress surface.
// It is a fixed carrier of the pieces transfer consumes, not a service
// locator, so nothing beyond those four is added.
type RunDeps struct {
	HTTP     *httpx.Client
	URL      URLProvider
	Observer Observer

	// Progress, when non-nil, receives the running byte counts of the tasks in
	// flight (review S-ETA2). A nil registry is skipped entirely: the run
	// builds no wrapper around the response bodies, so a transfer without one
	// behaves exactly as it did before the surface existed.
	Progress *Progress
}
