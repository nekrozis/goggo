// Package auth provides Galaxy credential persistence and token refresh: Store
// reads and writes the private credential store on disk (a non-plaintext binary
// envelope, saved atomically with mode 0600), and Refresh exchanges the stored
// refresh token at auth.gog.com/token for a new access token.
//
// The package never reads stdin/stdout, never builds its own transport (callers
// inject an *httpx.Client), and never persists implicitly: Refresh updates the
// in-memory store only, and the caller decides when to save it.
package auth
