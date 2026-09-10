package cli

import (
	"context"
	"errors"
	"io/fs"
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

// login runs the interactive login flow: credentials come from the flags when
// both are set, otherwise from the prompt (downloader.cpp:249-276).
func (s *Session) login(ctx context.Context, ui *console) error {
	email, password := s.Config.Email, s.Config.Password
	if email == "" || password == "" {
		var err error
		email, password, err = ui.promptCredentials()
		if err != nil {
			return err
		}
	}

	challenge, err := s.Web.Login(ctx, email, password, webapi.LoginOptions{
		ForceBrowser: s.Config.ForceBrowserLogin,
	})
	if err != nil {
		return err
	}
	if challenge != nil {
		if err := ui.resolveChallenge(ctx, s.Web, challenge); err != nil {
			return err
		}
	}

	// Persist what the login produced: the token file and the cookie jar
	// (the C++ COOKIELIST FLUSH).
	if err := auth.SaveTokenFile(s.Galaxy, s.Galaxy.GetFilepath()); err != nil {
		return err
	}
	_, err = s.HTTP.SaveCookies()
	return err
}

// Close flushes the cookie jar, mirroring the C++ flush on the way out.
func (s *Session) Close() error {
	_, err := s.HTTP.SaveCookies()
	return err
}
