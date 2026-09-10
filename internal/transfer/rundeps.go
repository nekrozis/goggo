package transfer

import (
	"github.com/nekrozis/goggo/internal/httpx"
)

// RunDeps is the construction-time dependency bundle the run loop takes. It is
// a fixed carrier of the three things transfer needs from outside — the HTTP
// exit, the URL seam and the event sink — not a service locator, so nothing
// else is added.
//
// The run loop itself arrives in the next step, which is why this type exists
// before its only consumer does.
type RunDeps struct {
	HTTP     *httpx.Client
	URL      URLProvider
	Observer Observer
}
