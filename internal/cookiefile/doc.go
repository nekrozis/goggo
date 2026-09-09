// Package cookiefile is the pure format codec for Netscape/Mozilla
// cookies.txt files (the format curl CURLOPT_COOKIEJAR writes and
// CURLOPT_COOKIEFILE reads; used by lgogdownloader for session cookies).
//
// Responsibilities (S10b-1 review lock):
//   - Parse: cookies.txt bytes -> []PersistentCookie
//   - Write: []PersistentCookie -> cookies.txt bytes
//
// This package is ONLY a format codec. It does not talk to a CookieJar, does
// not implement RFC 6265 matching, and does not normalise domain semantics:
// the Domain field is kept verbatim (leading dot, case) on both directions.
// Mapping PersistentCookie onto the standard net/http/cookiejar (host-only,
// path defaults, request matching) belongs to the httpx bridge (S10b-2).
//
// Semantic scope (review lock): Netscape cookies.txt cannot express every
// modern http.Cookie attribute (SameSite, Partitioned, ...). This codec
// promises compatibility with the fields the original project's session
// depends on (domain / includeSubdomains / path / secure / expiry / name /
// value). HttpOnly is carried through the curl "#HttpOnly_" domain-prefix
// extension: it is a persistable Netscape extension and never participates
// in cookiejar request matching.
//
// HostOnly mapping (review lock): the file's second column is the Netscape
// includeSubdomains flag; the codec maps it one-to-one onto
// PersistentCookie.HostOnly (TRUE => HostOnly=false, FALSE => HostOnly=true)
// and writes it back the same way. No extra validation is performed here:
// semantic consistency (e.g. a HostOnly cookie whose domain carries a
// leading dot) is the bridge's concern.
package cookiefile
