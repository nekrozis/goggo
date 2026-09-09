// Package auth provides the Galaxy credential persistence and refresh
// primitives, ported from the token lifecycle spread across the C++ codebase
// (galaxyapi.cpp refreshLogin/isTokenExpired, downloader.cpp token-file
// load/save, config.h GalaxyConfig).
//
// Scope (S10a):
//   - SaveTokenFile / LoadTokenFile: JSON token store on disk, written with
//     an atomic 0600 replace (S10a review lock: the temp file is created
//     0600 from the start, never truncated in place).
//   - Refresh (method on Client): authorization-code refresh against
//     auth.gog.com/token using grant_type=refresh_token, mirroring
//     galaxyapi.cpp:57-77.
//
// API shape (review lock): stateless token-file operations are package
// functions; refresh is a method on Client because it needs an httpx
// transport and an endpoint (instance-level tokenURL, no package-global
// mutable state). Callers construct Client with NewClient(hx); the token URL
// defaults to the production endpoint and tests override the unexported
// field.
//
// Layering boundary (review lock): auth holds ONLY primitives. Deciding
// whether a session should refresh or re-login is application orchestration
// (C++ Downloader::init/isLoggedIn); user interaction (2FA codes, browser
// paste) belongs to the CLI. S12 temporarily hosts the orchestration until
// the application/core layer exists — this is not permanently a CLI duty.
// auth never touches stdin/stdout, never builds its own transport (callers
// inject *httpx.Client), and never persists implicitly: Refresh updates the
// in-memory GalaxyConfig only, and callers decide when to SaveTokenFile.
//
// Cookie-file persistence is deliberately NOT here: it needs its own design
// (S10b) because the stdlib cookiejar offers no full snapshot API and the
// Netscape cookies.txt round-trip is lossy for RFC 6265 state.
package auth
