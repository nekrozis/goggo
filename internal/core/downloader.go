package core

import (
	"context"
	"errors"
	"fmt"

	"github.com/nekrozis/goggo/internal/auth"
	"github.com/nekrozis/goggo/internal/config"
	"github.com/nekrozis/goggo/internal/galaxy"
	"github.com/nekrozis/goggo/internal/httpx"
	"github.com/nekrozis/goggo/internal/webapi"
)

// Downloader is the state one run needs: the effective configuration, the front
// end it reports through, the transport (which owns the cookie jar), the two
// protocol clients and the Galaxy credential store.
//
// Fields are ordered to minimise padding: the config first, then the interface
// and the pointers, then the bool.
//
// The C++ class also carries a public curl handle, a Timer, a progressbar, a
// TimeAndSize pair, the resume position and the retry counters. None of them is
// copied: they are transfer-time state whose consumers (the download engine)
// are not ported yet. State is added when its first consumer arrives.
type Downloader struct {
	cfg config.Config
	ui  Console

	http   *httpx.Client
	web    *webapi.Client
	galaxy *galaxy.Client

	token *config.GalaxyConfig

	loggedIn bool
}

// Config returns the effective configuration.
//
// It exists for the one caller that still assembles its own work: the front
// end's listing command fetches its data itself until that moves into this
// package (review ruling D17=b). It goes away with that move.
func (d *Downloader) Config() config.Config { return d.cfg }

// Web exposes the website client for the same transitional reason as Config:
// the listing command in the front end still queries it directly.
func (d *Downloader) Web() *webapi.Client { return d.web }

// LoggedIn reports what Open established: the website session is valid and the
// Galaxy access token has not expired.
//
// Difference (recorded in the audit): the C++ isLoggedIn() probes the website
// and may refresh the token on every call (downloader.cpp:179-198); this port
// probes once, inside Open, and reports that result. Repeating the probe would
// add a request the C++ source makes no more than once per run.
func (d *Downloader) LoggedIn() bool { return d.loggedIn }

// errNoToken is returned by Init when there is no usable token and refreshing
// it produced nothing: the C++ init() reports the same outcome by returning 0,
// which makes the front end stop with exit code 1 (main.cpp:802-806).
var errNoToken = errors.New("galaxy: no valid access token and the refresh failed")

// Init mirrors Downloader::init (downloader.cpp:201-241): make sure a usable
// access token is in hand before any command runs.
//
// The C++ source calls this AFTER the login phase and BEFORE the command
// dispatch (main.cpp:802), where the token is normally fresh already, so the
// refresh below is a safety net rather than the main path. The report file
// (downloader.cpp:229-239) is registered, not ported: it belongs to the
// download engine.
func (d *Downloader) Init(ctx context.Context) error {
	if d.token.IsExpired() { // galaxyAPI::init() is exactly !isTokenExpired() (galaxyapi.cpp:43-56)
		if err := d.refreshAndSave(ctx); err != nil {
			return fmt.Errorf("%w: %v", errNoToken, err)
		}
	}

	// Kept structurally, exactly as the C++ source has it: a stored payload
	// that is expired gets one more refresh, and a failure there is silent.
	// After the branch above the token is fresh, so this is unreachable in
	// practice; it stays because removing it would depart from the original
	// sequence for no gain.
	if len(d.token.GetJSON()) != 0 && d.token.IsExpired() {
		_ = d.refreshAndSave(ctx)
	}
	return nil
}

// refreshAndSave mirrors galaxyAPI::refreshLogin followed by
// Downloader::saveGalaxyJSON (galaxyapi.cpp:57-83, downloader.cpp:3792-3810).
func (d *Downloader) refreshAndSave(ctx context.Context) error {
	if err := auth.NewClient(d.http).Refresh(ctx, d.token); err != nil {
		return err
	}
	return auth.SaveTokenFile(d.token, d.token.GetFilepath())
}
