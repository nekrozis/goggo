package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	"github.com/nekrozis/goggo/internal/auth"
	"github.com/nekrozis/goggo/internal/config"
	"github.com/nekrozis/goggo/internal/httpx"
	"github.com/nekrozis/goggo/internal/webapi"
)

// Session is the state one run needs: the website client (which owns the
// transport and the cookie jar), the Galaxy credential store and whether the
// account is usable.
//
// Fields are ordered to minimise padding: the pointers and the config first,
// then the bool.
//
// This is the temporary home of the orchestration the C++ original keeps in
// Downloader (downloader.cpp:179-237 and its constructor). When internal/core
// is ported (S17) it moves there; nothing else in this package should grow into
// a second orchestration layer in the meantime.
type Session struct {
	Config config.Config
	HTTP   *httpx.Client
	Web    *webapi.Client
	Galaxy *config.GalaxyConfig

	// LoggedIn mirrors Downloader::isLoggedIn(): the website session is valid
	// AND the Galaxy access token is not expired.
	LoggedIn bool
}

// Open prepares a session: it loads the persisted cookies and token, refreshes
// the token when it has expired and, unless allowLogin is false, runs the login
// flow when the account is not usable.
//
// allowLogin exists because the C++ front end answers --check-login-status
// before it ever considers logging in (main.cpp:685-698).
func Open(ctx context.Context, cfg config.Config, ui *console, allowLogin bool) (*Session, error) {
	// Directories first: every persistence path below writes into them.
	if err := ensureDirectories(cfg); err != nil {
		return nil, err
	}
	galaxy := config.NewGalaxyConfig()
	// The session owns the transport: the login flow's cookies live in this
	// client's jar, and the same handle persists them.
	hx, err := httpx.New(httpxCfg(cfg))
	if err != nil {
		return nil, err
	}
	web, err := webapi.New(hx, galaxy)
	if err != nil {
		return nil, err
	}

	s := &Session{Config: cfg, HTTP: hx, Web: web, Galaxy: galaxy}
	if err := hx.LoadCookies(); err != nil {
		return nil, err
	}
	if err := auth.LoadTokenFile(s.Galaxy, tokenPath(cfg)); err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		// No token file yet: a fresh install, exactly like the C++ loader
		// leaving an empty store.
	}
	s.Galaxy.SetFilepath(tokenPath(cfg))

	if s.Galaxy.IsExpired() && s.Galaxy.GetRefreshToken() != "" {
		if err := auth.NewClient(hx).Refresh(ctx, s.Galaxy); err == nil {
			_ = auth.SaveTokenFile(s.Galaxy, s.Galaxy.GetFilepath())
		}
		// A failed refresh is not fatal here: the login flow below decides.
	}

	s.LoggedIn = s.checkLoggedIn(ctx)
	if allowLogin && (cfg.Login || !s.LoggedIn) {
		if err := s.login(ctx, ui); err != nil {
			return nil, err
		}
		s.LoggedIn = true
	}
	return s, nil
}

// httpxCfg maps the CLI configuration onto the transport configuration.
func httpxCfg(cfg config.Config) httpx.Config {
	return httpx.Config{
		UserAgent:          cfg.Curl.UserAgent,
		CACertPath:         cfg.Curl.CACertPath,
		CookieFile:         cfg.Curl.CookiePath,
		InsecureSkipVerify: !cfg.Curl.VerifyPeer,
		Timeout:            time.Duration(cfg.Curl.Timeout) * time.Second,
		// The website retry rule (min(3, retries) additional attempts) stays a
		// webapi concern; Wait keeps the C++ unit, microseconds — see the audit
		// note on --wait.
		RetryPolicy: webapi.RetryPolicyFor(cfg.Retries, time.Duration(cfg.Wait)*time.Microsecond),
		// The transfer guard mirrors --lowspeed-timeout / --lowspeed-rate; the
		// C++ field names cross over: LowSpeedTimeout is the duration in
		// seconds, LowSpeedTimeoutRate the rate in bytes per second.
		LowSpeedLimit: cfg.Curl.LowSpeedTimeoutRate,
		LowSpeedTime:  time.Duration(cfg.Curl.LowSpeedTimeout) * time.Second,
	}
}

// tokenPath is the Galaxy token store location (main.cpp:81).
func tokenPath(cfg config.Config) string {
	return cfg.ConfigDirectory + "/galaxy_tokens.json"
}

// checkLoggedIn mirrors Downloader::isLoggedIn (downloader.cpp:179-198).
func (s *Session) checkLoggedIn(ctx context.Context) bool {
	ok, err := s.Web.IsLoggedIn(ctx)
	if err != nil {
		return false
	}
	return ok && !s.Galaxy.IsExpired()
}

// ensureDirectories creates the per-user directories the program writes to,
// mirroring main.cpp:377-406 (the XML, configuration and cache directories).
//
// The token, cookie and configuration files all live under the configuration
// directory and their writers create temporary files there, so it has to exist
// before the first persistence — the C++ front end creates it during startup
// for the same reason.
//
// The mode is Unix semantics. On Windows the permission bits carry no
// filesystem meaning; there the requirement is simply that the directory gets
// created.
func ensureDirectories(cfg config.Config) error {
	for _, dir := range []string{cfg.XMLDirectory, cfg.ConfigDirectory, cfg.CacheDirectory} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create directory %q: %w", dir, err)
		}
	}
	return nil
}

// credentials resolves the credentials the login flow needs. It follows the
// branch structure of Downloader::login (downloader.cpp:249-288):
//
//   - a supplied pair (flags or configuration) is used as it is;
//   - with --browser-login the credentials are irrelevant, so nothing is asked
//     for and an empty pair is not an error (downloader.cpp:254,376);
//   - otherwise a non-terminal input takes the headless branch, which names the
//     files it expects instead of prompting. A PARTIALLY supplied pair takes
//     that branch too: the C++ tests the pair before anything else
//     (downloader.cpp:249), so a lone --login-email is not used without a
//     terminal either;
//   - otherwise each MISSING value is asked for on demand — an intentional
//     difference from the C++, which prompts for both unless both flags are set
//     (review ruling B, S12.2-R1);
//   - and a value still empty is reported the way upstream reports it
//     (downloader.cpp:282-288).
//
// interactive is passed in rather than read from ui here so the branch
// structure is testable without a terminal; the decision itself lives in one
// place, console.terminalFd.
func credentials(cfg config.Config, ui *console, interactive bool) (email, password string, err error) {
	email, password = cfg.Email, cfg.Password
	if email != "" && password != "" {
		return email, password, nil
	}
	if cfg.ForceBrowserLogin {
		return email, password, nil
	}
	if !interactive {
		return "", "", headlessCredentials(cfg, ui)
	}
	if email == "" {
		if email, err = ui.promptEmail(); err != nil {
			return "", "", err
		}
	}
	if password == "" {
		if password, err = ui.promptPassword(); err != nil {
			return "", "", err
		}
	}
	if email == "" || password == "" {
		return "", "", errors.New("Email and/or password empty")
	}
	return email, password, nil
}

// headlessCredentials mirrors the non-terminal branch of Downloader::login
// (downloader.cpp:256-265): with no terminal there is nobody to prompt, so the
// cookie file and the token file it would have used are printed to stdout and
// the login gives up.
//
// Intentional differences, both review rulings:
//
//   - Q1=b: where the C++ source goes on to log in with the empty credential
//     pair when both files exist, this port stops. An empty credential pair is
//     never posted, so this branch always ends in an explicit failure.
//   - ①: the failure message is the same whether or not the two files exist,
//     because both cases leave the caller with the same job — supply
//     credentials. The behavioural difference the C++ source attached to the
//     file check lives in the code above, not in the wording. ② keeps the hint
//     actionable: --login cannot prompt here either, so the flags are named.
func headlessCredentials(cfg config.Config, ui *console) error {
	fmt.Fprintln(ui.out, cfg.Curl.CookiePath)
	fmt.Fprintln(ui.out, tokenPath(cfg))
	return errors.New("no credentials available in a non-interactive session; " +
		"run --login in a terminal, or pass --login-email/--login-password")
}

// login runs the login flow (downloader.cpp:243-326), reporting progress on
// stderr the way the C++ source does.
func (s *Session) login(ctx context.Context, ui *console) error {
	email, password, err := credentials(s.Config, ui, ui.isTerminal())
	if err != nil {
		return err
	}

	challenge, err := s.Web.Login(ctx, email, password, webapi.LoginOptions{
		ForceBrowser: s.Config.ForceBrowserLogin,
	})
	if err != nil {
		return fmt.Errorf("Galaxy: Login failed: %w", err)
	}
	if challenge != nil {
		if err := ui.resolveChallenge(ctx, s.Web, challenge); err != nil {
			// An unfinished challenge leaves the flow without tokens, which
			// the C++ source also reports as a Galaxy login failure
			// (website.cpp:345-348, downloader.cpp:300-304).
			return fmt.Errorf("Galaxy: Login failed: %w", err)
		}
	}
	fmt.Fprintln(ui.errOut, "Galaxy: Login successful")

	// Persist what the login produced: the token file (saveGalaxyJSON,
	// downloader.cpp:308-312) and the cookie jar (the C++ COOKIELIST FLUSH).
	if err := auth.SaveTokenFile(s.Galaxy, s.Galaxy.GetFilepath()); err != nil {
		return err
	}

	// The website session probe that decides HTTP login success
	// (downloader.cpp:314-322).
	ok, err := s.Web.IsLoggedIn(ctx)
	if err != nil {
		return fmt.Errorf("HTTP: Login failed: %w", err)
	}
	if !ok {
		return errors.New("HTTP: Login failed")
	}
	fmt.Fprintln(ui.errOut, "HTTP: Login successful")

	_, err = s.HTTP.SaveCookies()
	return err
}

// Close flushes the cookie jar, mirroring the C++ flush on the way out.
func (s *Session) Close() error {
	_, err := s.HTTP.SaveCookies()
	return err
}
