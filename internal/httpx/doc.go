// Package httpx is the transport layer: net/http plus the behaviour every
// caller of the GOG APIs needs — one retry policy, one User-Agent, one cookie
// jar and a low-speed guard.
//
// The cookie jar lives for the whole Client lifetime, so a cookie received by
// one request is sent with later ones; with Config.CookieFile set the jar can be
// persisted through LoadCookies and SaveCookies. A caller that drives redirects
// itself (the login flow) uses DoNoRedirect, a per-call behaviour rather than a
// client-wide mode.
package httpx
