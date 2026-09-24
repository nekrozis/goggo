package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/nekrozis/goggo/internal/auth"
	"github.com/nekrozis/goggo/internal/config"
	"github.com/nekrozis/goggo/internal/galaxy"
	"github.com/nekrozis/goggo/internal/httpx"
	"github.com/nekrozis/goggo/internal/webapi"
)

// SessionRequest is what a run needs from the session before its command can
// work. The two fields are independent on purpose: a read-only command needs a
// session but must never create one, while an explicit login creates one
// without needing anything first.
//
// The zero value asks for no session at all: no missing-session failure, no
// login attempt.
type SessionRequest struct {
	// Required makes a missing session fatal: instead of handing back a run
	// that cannot work, OpenWith reports ErrSessionRequired.
	Required bool

	// AllowLogin lets this run perform the login flow. The front end sets it
	// for the commands that write, and only when a terminal can answer the
	// prompts — so an implicit login can never reach the headless branch of
	// credentials, whose diagnostic is written for an explicit login.
	AllowLogin bool
}

// ErrSessionRequired is the one failure a session-requiring command has when
// there is no usable session and this run may not log in.
//
// The wording is the hint the CLI contract promises: the user's
// next step is the login command, not a retry. It names no command of its own,
// so every command reports the same sentence instead of inventing its own.
var ErrSessionRequired = errors.New("not logged in; run `goggo auth login`")

// ErrSessionUnconfirmed is the sibling failure for the state the probe could
// not answer: the request to the website itself failed, so whether a session
// exists is unknown. It is not a credential problem and must not borrow
// ErrSessionRequired's advice — the session may be perfectly good, and the
// network is what to retry.
var ErrSessionUnconfirmed = errors.New("cannot confirm the login session")

// OpenWith is the session opener with the outside pieces supplied (see
// Dependencies). Only the network exit of the transport can differ, which is
// what makes this seam worth having; a caller with nothing to replace passes
// the zero Dependencies.
func OpenWith(ctx context.Context, cfg config.Config, ui Console, req SessionRequest,
	deps Dependencies) (*Downloader, error) {
	// Directories first: every persistence path below writes into them.
	if err := ensureDirectories(cfg); err != nil {
		return nil, err
	}
	store, err := auth.Open(auth.StorePath(cfg))
	if err != nil {
		return nil, err
	}
	// The session owns the transport: the login flow's cookies live in this
	// client's jar, and the same handle persists them.
	hx, err := httpx.New(httpxCfg(cfg, deps))
	if err != nil {
		return nil, err
	}
	web, err := webapi.New(hx, store)
	if err != nil {
		return nil, err
	}
	gx, err := galaxy.New(hx, store)
	if err != nil {
		return nil, err
	}

	d := &Downloader{cfg: cfg, ui: ui, http: hx, web: web, galaxy: gx, progress: deps.Progress, token: store}
	if err := hx.LoadCookies(); err != nil {
		return nil, err
	}

	// An expired store with a session to renew is refreshed before anything
	// asks for a credential; Refresh reports a store with no refresh token
	// without a request, and a failed refresh is not fatal here — the login
	// flow below decides.
	if store.Expired() && !store.Empty() {
		if err := store.Refresh(ctx, auth.NewClient(hx)); err == nil {
			_ = store.Save()
		} else {
			// The failure is kept as an observation, not returned: the login
			// flow below still decides the run, and a command that answers
			// without a session (the status report) must be able to say WHY
			// the session could not be renewed instead of dropping the reason.
			d.apiSessionDiag = err.Error()
		}
	}

	d.loggedIn = d.checkLoggedIn(ctx)
	if req.AllowLogin && (cfg.Login || !d.loggedIn) {
		if err := d.Login(ctx); err != nil {
			return nil, err
		}
		// A completed login is definite evidence of a session: the stale
		// probe failure must not survive it.
		d.loggedIn = true
		d.probeErr = nil
	}
	// A command that needs a session and was not allowed to create one fails
	// here, once, with the action the user can take. Letting the command fail
	// on its own would report a protocol error that reads like a network
	// fault, mixing "no local session" with "the API is broken".
	if req.Required && !d.loggedIn {
		if d.probeErr != nil {
			return nil, fmt.Errorf("%w: %v", ErrSessionUnconfirmed, d.probeErr)
		}
		return nil, ErrSessionRequired
	}
	return d, nil
}

// httpxCfg maps the CLI configuration onto the transport configuration.
//
// Only the network exit can be replaced (deps.HTTPTransport): everything else —
// the cookie file, the retry policy, the low-speed guard — is the production
// configuration, so a run driven through the seam persists its session exactly
// like a normal one.
func httpxCfg(cfg config.Config, deps Dependencies) httpx.Config {
	return httpx.Config{
		UserAgent:          cfg.Curl.UserAgent,
		CACertPath:         cfg.Curl.CACertPath,
		CookieFile:         cfg.Curl.CookiePath,
		Transport:          deps.HTTPTransport,
		InsecureSkipVerify: !cfg.Curl.VerifyPeer,
		Timeout:            time.Duration(cfg.Curl.Timeout) * time.Second,
		// The website retry rule (min(3, retries) additional attempts) stays a
		// webapi concern; the wait between attempts comes from retryWait.
		RetryPolicy: webapi.RetryPolicyFor(cfg.Retries, retryWait(cfg)),
		// The transfer guard: LowSpeedTimeout is a duration in seconds,
		// LowSpeedTimeoutRate a rate in bytes per second.
		LowSpeedLimit: cfg.Curl.LowSpeedTimeoutRate,
		LowSpeedTime:  time.Duration(cfg.Curl.LowSpeedTimeout) * time.Second,
	}
}

// retryWait is the single place the website retry wait is built from the
// configuration: the unit exposed by the CLI is milliseconds, and this is where
// it becomes a time.Duration. The transfer path converts the same way
// (install.go's Options.Wait).
func retryWait(cfg config.Config) time.Duration {
	return time.Duration(cfg.Wait) * time.Millisecond
}

// checkLoggedIn probes the website session and requires an unexpired Galaxy
// token. A probe that could not answer is not an answer: the failure travels
// as state (probeErr) so the Required gate and the status report can say
// "unknown" instead of pointing at a credential that may be fine.
func (d *Downloader) checkLoggedIn(ctx context.Context) bool {
	ok, err := d.web.IsLoggedIn(ctx)
	if err != nil {
		d.probeErr = err
		return false
	}
	d.probeErr = nil
	return ok && !d.token.Expired()
}

// ensureDirectories creates the per-user directories the program writes to: the
// XML, configuration and cache directories. The token, cookie and configuration
// files all live under the configuration directory and their writers create
// temporary files there, so it has to exist before the first persistence.
//
// The mode is Unix semantics: on Windows the permission bits carry no filesystem
// meaning, only that the directory gets created.
func ensureDirectories(cfg config.Config) error {
	for _, dir := range []string{cfg.XMLDirectory, cfg.ConfigDirectory, cfg.CacheDirectory} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create directory %q: %w", dir, err)
		}
	}
	return nil
}

// credentials resolves the credentials the login flow needs: the email may be
// supplied (the --email flag); the password never is, it only ever comes from the
// prompt. With --browser-login the credentials are irrelevant, so an empty pair
// is not an error; otherwise a non-terminal input takes the headless branch,
// which names the files it expects instead of prompting — and an email supplied
// without a terminal takes that branch too, since a lone --email is not used
// without a terminal; otherwise each MISSING value is asked for on demand, and a
// value still empty is an error.
func credentials(cfg config.Config, ui Console, interactive bool) (email, password string, err error) {
	email = cfg.Email
	if cfg.ForceBrowserLogin {
		return email, password, nil
	}
	if !interactive {
		return "", "", headlessCredentials(cfg, ui)
	}
	if email == "" {
		if email, err = ui.PromptEmail(); err != nil {
			return "", "", err
		}
	}
	if password, err = ui.PromptPassword(); err != nil {
		return "", "", err
	}
	if email == "" || password == "" {
		return "", "", errors.New("Email and/or password empty")
	}
	return email, password, nil
}

// headlessCredentials is the non-terminal branch of the login flow: with no
// terminal there is nobody to prompt, so the cookie file and the token file it
// would have used are printed to stdout and the login gives up. An empty
// credential pair is never posted, so the branch always ends in an explicit
// failure; the message is the same whether or not the two files exist, because
// both cases leave the caller with the same job — supply credentials.
//
// Only an explicit login request reaches this branch: an implicit login is
// allowed solely when a terminal can answer it (see SessionRequest.AllowLogin).
func headlessCredentials(cfg config.Config, ui Console) error {
	fmt.Fprintln(ui.Out(), cfg.Curl.CookiePath)
	fmt.Fprintln(ui.Out(), auth.StorePath(cfg))
	return errors.New("no credentials available in a non-interactive session; " +
		"run `goggo auth login` in a terminal")
}

// Login runs the login flow, reporting progress on stderr.
func (d *Downloader) Login(ctx context.Context) error {
	email, password, err := credentials(d.cfg, d.ui, d.ui.IsTerminal())
	if err != nil {
		return err
	}

	challenge, err := d.web.Login(ctx, email, password, webapi.LoginOptions{
		ForceBrowser: d.cfg.ForceBrowserLogin,
	})
	if err != nil {
		return fmt.Errorf("Galaxy: Login failed: %w", err)
	}
	if challenge != nil {
		if err := d.ui.ResolveChallenge(ctx, d.web, challenge); err != nil {
			// An unfinished challenge leaves the flow without tokens, which is
			// reported as a Galaxy login failure.
			return fmt.Errorf("Galaxy: Login failed: %w", err)
		}
	}
	fmt.Fprintln(d.ui.ErrOut(), "Galaxy: Login successful")

	// Persist what the login produced: the token file and the cookie jar.
	if err := d.token.Save(); err != nil {
		return err
	}

	// The website session probe that decides HTTP login success.
	ok, err := d.web.IsLoggedIn(ctx)
	if err != nil {
		return fmt.Errorf("HTTP: Login failed: %w", err)
	}
	if !ok {
		return errors.New("HTTP: Login failed")
	}
	fmt.Fprintln(d.ui.ErrOut(), "HTTP: Login successful")

	_, err = d.http.SaveCookies()
	return err
}

// Close flushes the cookie jar.
func (d *Downloader) Close() error {
	_, err := d.http.SaveCookies()
	return err
}
