// Package httpx is the transport layer: net/http plus the behaviour every
// caller of the GOG APIs needs — one retry policy, one User-Agent, one cookie
// jar and a low-speed guard.
//
// Layering:
//
//	error.go     StatusError (HTTP >= 400) and IsNotFound/IsForbidden probes
//	client.go    Client: transport/TLS/UA/cookie jar/timeout + Do/Get/GetBytes
//	retry.go     RetryPolicy + DoWithRetry decorator (independent of Client)
//	safeerror.go renders an error without the request URL
//
// The cookie jar lives for the whole Client lifetime, so a cookie received by
// one request is sent with later ones; with Config.CookieFile set the jar can
// be persisted through LoadCookies and SaveCookies. A caller that has to drive
// redirects itself (the login flow) uses DoNoRedirect, which is a per-call
// behaviour rather than a client-wide mode.
package httpx
