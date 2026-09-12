package core

import (
	"net/http"

	"github.com/nekrozis/goggo/internal/transfer"
)

// Dependencies are the outside pieces a run takes, so a test can drive Open
// against a transport it controls and a front end can poll the download
// progress it asked for.
//
// It is a construction-time seam, not runtime configuration and not a client
// container: the network exit is replaceable and the progress surface is handed
// in, and nothing else leaves the caller's control. The transport, the webapi
// client and the Galaxy client stay built here, from the caller's
// configuration, because handing them in prebuilt would let a caller pair a
// webapi client with a different transport than the cookie jar this package
// persists through (the ownership rule of S12-R), and neither client exposes an
// interface to fake — a rewriting RoundTripper already covers every offline
// scenario.
//
// Fields are added only together with their first consumer.
type Dependencies struct {
	// HTTPTransport, when non-nil, replaces the network exit of the transport
	// OpenWith builds. The cookie jar, the cookie file, the retry policy and the
	// low-speed guard stay exactly what production uses, so a test that points
	// the production hosts at a local server exercises the same configuration
	// rather than a second, cookie-less one.
	HTTPTransport http.RoundTripper

	// Progress, when non-nil, is the registry an install run publishes its
	// per-task byte counts into (review S-ETA2). It is a plain carrier: this
	// package only passes it through to the transfer run, and the front end
	// that wants the counts polls the same pointer. A nil registry leaves the
	// run exactly as it was before the surface existed.
	Progress *transfer.Progress
}
