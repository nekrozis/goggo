// Package galaxy speaks the GOG Galaxy content API: the endpoints serving
// builds, manifests, depot data, secure links and product documents for a
// product, authenticated with the OAuth access token rather than with the
// website session cookie.
//
// It owns no transport — the caller passes in one *httpx.Client, so the retry
// policy, timeouts, the low-speed guard and the User-Agent are configured in one
// place — and it never refreshes the token, which stays with internal/auth and
// the caller. Documents are returned as decoded JSON; mapping them onto domain
// types belongs to internal/gamedetails.
package galaxy
