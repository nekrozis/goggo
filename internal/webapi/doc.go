// Package webapi ports the GOG website/OAuth login flow of lgogdownloader
// (src/website.cpp:306-695) onto the transport layer in internal/httpx.
//
// Scope (S09a):
//   - Login: authorization-code exchange against auth.gog.com, including the
//     login_check POST and manual 3xx redirect chain that extracts the code.
//   - Two-step / TOTP challenges and browser-assisted login are expressed as
//     challenges (LoginChallenge): webapi decides WHAT information the login
//     needs; the CLI layer decides HOW to obtain it from the user. Login
//     never touches stdin/stdout/os.Exit. The caller completes the flow with
//     ContinueLogin once the user supplies the code or callback URL.
//   - IsLoggedIn (the C++ IsloggedInSimple account-page probe).
//
// Deferred to later steps (recorded in dev/audit/S09a.md):
//   - getGames / getGameDetailsJSON / getWishlistItems / getTags /
//     getOwnedGamesIds (website.cpp:83-305,696-858) -> list assembly (S11).
//   - Cookie file persistence (CURLOPT_COOKIEJAR / COOKIELIST FLUSH) -> S10.
//
// Integration notes for the reviewer:
//   - Login endpoint hosts are configurable per Client (endpoints) so the
//     flow is testable against an httptest server; production defaults match
//     the hardcoded C++ URLs.
//   - Per-request retry cap is min(3, Retries) extra attempts, mirroring
//     getResponse (website.cpp:33) via the httpx RetryPolicy semantics.
//   - Redirects are driven manually with httpx.DoNoRedirect (per-call, not a
//     client-wide mode), mirroring CURLOPT_FOLLOWLOCATION=0 during login.
//   - Login form parsing uses golang.org/x/net/html v0.59.0 (pure Go; the
//     first third-party dependency, approved 2026-09-10; security floor >=
//     v0.55.0 per GO-2026-5027) in place of the C++ libtidy + tinyxml2 pair.
package webapi
