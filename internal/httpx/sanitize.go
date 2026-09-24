package httpx

import (
	"errors"
	"net/url"
	"slices"
	"strings"
)

// credentialParams are the query parameters whose VALUES are credentials. They
// are dropped from any URL that reaches an error, because the download URLs of
// both chains are signed: the website chain's downlink URL and the Galaxy CDN
// chunk URL carry the session's access token in the query string.
//
// The list holds only credential-bearing names. Diagnostic parameters — path,
// client_id, grant_type, redirect_uri and the rest — are deliberately absent:
// removing them would cost the message its usefulness without protecting
// anything.
var credentialParams = []string{
	"access_token",
	"refresh_token",
	"token",
	"code",
	"client_secret",
}

// SanitizeURL removes the credential-bearing query parameters from rawURL and
// leaves everything else exactly as it was: the path, the fragment, the order
// of the remaining parameters and their percent-encoding are preserved byte for
// byte.
//
// The query is split into parameter pairs and a pair is dropped only when its
// key equals one of credentialParams exactly, so "token_x" survives while every
// "token" is dropped. Re-encoding through url.Values would be shorter to write
// but wrong: it reorders the parameters and normalises their encoding, and a
// diagnostic that no longer matches the request it describes is worse than none.
func SanitizeURL(rawURL string) string {
	q := strings.IndexByte(rawURL, '?')
	if q < 0 {
		return rawURL
	}
	head, query := rawURL[:q+1], rawURL[q+1:]

	// A fragment is not part of the query, but it can follow one.
	fragment := ""
	if h := strings.IndexByte(query, '#'); h >= 0 {
		fragment, query = query[h:], query[:h]
	}
	if query == "" {
		return rawURL
	}

	pairs := strings.Split(query, "&")
	kept := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		key, _, _ := strings.Cut(pair, "=")
		if isCredentialParam(key) {
			continue
		}
		kept = append(kept, pair)
	}
	if len(kept) == len(pairs) {
		return rawURL
	}
	return head + strings.Join(kept, "&") + fragment
}

// isCredentialParam reports whether key names a credential parameter.
func isCredentialParam(key string) bool {
	return slices.Contains(credentialParams, key)
}

// SanitizeError removes credential-bearing URLs from err while preserving its
// type and its whole chain.
//
// It is the boundary for the two failure classes that never reach a
// *StatusError: http.NewRequestWithContext reports an unparsable URL as a
// *url.Error, and the transport reports a broken connection the same way. Both
// carry the full request URL, and both can therefore carry a session token.
//
// The URL is replaced IN PLACE rather than by building a new error, because a
// rebuilt error would either lose the wrapper above it or duplicate it. Editing
// the field keeps every property callers depend on: errors.Is still reaches
// context.Canceled and context.DeadlineExceeded through Err, errors.As still
// finds the *url.Error, and Timeout and Unwrap still answer. It also makes the
// function idempotent — a second call finds the URL already clean and returns
// err untouched — and a *StatusError, which NewStatusError has already
// sanitised, is not a *url.Error at all, so it passes through unwrapped.
//
// One consequence of editing in place is worth stating: an error that has
// ALREADY been wrapped cannot be repaired. fmt.Errorf formats its message when
// it wraps, so a wrapper built around a raw *url.Error keeps the raw URL in its
// text no matter what happens to the inner value afterwards. That is why this
// is a BOUNDARY and not a cleanup: every site applies it to the error it just
// received from the transport, before anything wraps it. Sanitising later is not
// a substitute — see TestSanitizeErrorCannotRepairAnAlreadyWrappedError.
func SanitizeError(err error) error {
	if err == nil {
		return nil
	}
	var urlErr *url.Error
	if !errors.As(err, &urlErr) {
		return err
	}
	clean := SanitizeURL(urlErr.URL)
	if clean == urlErr.URL {
		return err
	}
	urlErr.URL = clean
	return err
}
