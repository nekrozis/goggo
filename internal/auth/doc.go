// Package auth provides Galaxy credential persistence and token refresh.
//
// SaveTokenFile and LoadTokenFile read and write the JSON token store on disk;
// a save replaces the file atomically with mode 0600. Refresh (a method on
// Client) exchanges the stored refresh token at auth.gog.com/token for a new
// access token and stores the response in the in-memory config.GalaxyConfig.
//
// The package holds only these primitives: it never reads stdin/stdout, never
// builds its own transport (callers inject an *httpx.Client), and never persists
// implicitly — Refresh updates the in-memory config only, and the caller decides
// when to save it.
package auth
