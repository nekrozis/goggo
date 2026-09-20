// Package cookiefile is the format codec for Netscape/Mozilla cookies.txt files.
//
// Parse decodes cookies.txt bytes into []PersistentCookie and Write encodes them
// back. It is only a codec: it does not touch a CookieJar, does not implement
// RFC 6265 matching, and does not normalise domain semantics — Domain is kept
// verbatim (leading dot, case) in both directions, and the second column is the
// includeSubdomains flag mapped one-to-one onto HostOnly (TRUE => HostOnly=false).
// Mapping PersistentCookie onto net/http/cookiejar is the httpx bridge's job.
//
// HttpOnly is carried through the "#HttpOnly_" domain-prefix extension.
package cookiefile
