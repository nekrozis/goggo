package core

import (
	"context"
	"errors"
	"fmt"

	"github.com/nekrozis/goggo/internal/auth"
	"github.com/nekrozis/goggo/internal/config"
	"github.com/nekrozis/goggo/internal/galaxy"
	"github.com/nekrozis/goggo/internal/httpx"
	"github.com/nekrozis/goggo/internal/transfer"
	"github.com/nekrozis/goggo/internal/webapi"
)

// Downloader is the state one run needs: the effective configuration, the front
// end it reports through, the transport (which owns the cookie jar), the two
// protocol clients, the optional progress registry and the Galaxy credential
// store. State is added when its first consumer arrives.
type Downloader struct {
	cfg config.Config
	ui  Console

	http   *httpx.Client
	web    *webapi.Client
	galaxy *galaxy.Client

	// progress is the optional sampling surface an install run publishes into.
	// It is handed in through Dependencies and belongs to whoever polls it;
	// nil means the run reports no samples.
	progress *transfer.Progress

	token *auth.Store

	loggedIn bool

	// apiSessionDiag records why the Galaxy API session could not be renewed:
	// the one refresh OpenWith attempts, when it fails. It is an observation
	// only — the store, the token and the run's outcome are untouched by it.
	apiSessionDiag string

	// probeErr is the login probe's transport failure, nil when the probe
	// answered. (loggedIn, probeErr) is the tri-state of the website session:
	// true/false+nil/false+err read as logged in / definitely not / unknown.
	// Written once during Open, before any command runs, and read after —
	// the same unlocked lifetime loggedIn has always had.
	probeErr error
}

// SessionProbeErr reports why the website session could not be confirmed, or
// nil when the probe answered (in either direction).
func (d *Downloader) SessionProbeErr() error { return d.probeErr }

// APISessionDiag reports the stored refresh failure, or "" when no refresh
// was attempted or it succeeded. A local credential that never needed a
// refresh carries no evidence either way: absence of this diagnosis is not
// proof that the API accepts the credential.
func (d *Downloader) APISessionDiag() string { return d.apiSessionDiag }

// Config returns the effective configuration.
//
// It exists for the one caller that still assembles its own work: the front
// end's listing command fetches its data itself until that moves into this
// package. It goes away with that move.
func (d *Downloader) Config() config.Config { return d.cfg }

// Web exposes the website client for the same transitional reason as Config:
// the listing command in the front end still queries it directly.
func (d *Downloader) Web() *webapi.Client { return d.web }

// LoggedIn reports what Open established: the website session is valid and the
// Galaxy access token has not expired. The probe runs once, inside Open, so
// calling this repeatedly adds no requests.
func (d *Downloader) LoggedIn() bool { return d.loggedIn }

// errNoToken is returned by Init when there is no usable token and refreshing
// it produced nothing.
var errNoToken = errors.New("galaxy: no valid access token and the refresh failed")

// Init makes sure a usable access token is in hand before any command runs.
//
// The refresh is a safety net rather than the main path: Init runs after the
// login phase, where the token is normally fresh already.
func (d *Downloader) Init(ctx context.Context) error {
	if d.token.Expired() {
		if err := d.refreshAndSave(ctx); err != nil {
			return fmt.Errorf("%w: %v", errNoToken, err)
		}
	}

	// A stored payload that is still expired gets one more refresh, and a
	// failure there is silent. The branch above leaves the token fresh, so this
	// is unreachable in practice.
	if !d.token.Empty() && d.token.Expired() {
		_ = d.refreshAndSave(ctx)
	}
	return nil
}

// refreshAndSave refreshes the Galaxy token and persists it.
func (d *Downloader) refreshAndSave(ctx context.Context) error {
	if err := d.token.Refresh(ctx, auth.NewClient(d.http)); err != nil {
		return err
	}
	return d.token.Save()
}
