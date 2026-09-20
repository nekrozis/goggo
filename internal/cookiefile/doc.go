// Package cookiefile is the record codec for the cookie store's payload: Encode
// serialises []PersistentCookie into the bytes the store carries and Decode
// reads them back.
//
// It is only a codec — it does not touch a CookieJar, does not implement RFC 6265
// matching and does not normalise domain semantics (Domain is kept verbatim, leading
// dot and case included). Mapping onto net/http/cookiejar is the httpx bridge's job,
// and so is the container around the payload: the magic, the version, the
// obfuscation and the checksum are secretfile's, and httpx is what puts the two
// together.
package cookiefile
