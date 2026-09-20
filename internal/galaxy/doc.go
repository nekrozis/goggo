// Package galaxy speaks the GOG Galaxy content API: the endpoints serving
// builds, manifests, depot data, secure links and product documents for a
// product, authenticated with the OAuth access token rather than with the
// website session cookie.
//
// It is a library layer: it returns errors and never prints or exits. It owns
// no transport — the caller builds one *httpx.Client and passes it in, so the
// retry policy, timeouts, the low-speed guard and the User-Agent are configured
// in one place — and it never refreshes the token, which stays with
// internal/auth and the caller.
//
// "Authorization: Bearer" is attached only while the store reports the token
// unexpired; an expired or empty token means the request goes out without it.
// Documents are returned as decoded JSON, and mapping them onto domain types
// belongs to internal/gamedetails.
package galaxy
