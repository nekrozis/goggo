// Package webapi speaks the GOG website and OAuth endpoints over the transport
// layer in internal/httpx: the login flow and the account endpoints that need a
// website session rather than a Galaxy access token.
//
// Login performs the authorization-code exchange against auth.gog.com — the
// login_check POST and the manual 3xx redirect chain that extracts the code.
// When it needs user input it returns a LoginChallenge: webapi decides WHAT the
// login needs, the CLI decides HOW to obtain it, and the caller finishes with
// ContinueLogin. Endpoint hosts are per-Client fields, so the flow can be tested
// against an httptest server.
package webapi
