package httpx

import (
	"errors"
	"fmt"
	"net/url"
)

// SafeError renders err without the request URL.
//
// Transport errors embed the URL they were sent to: StatusError prints
// "GET <url>: HTTP 400", and the standard library's *url.Error prints
// `Get "<url>": <cause>`. For an endpoint whose query string carries
// credentials — the OAuth token endpoint takes client_secret, code and
// refresh_token — that would put a credential into logs, bug reports and paste
// buffers. Callers on those endpoints render the error through here instead of
// using %w.
//
// Only known-safe renderings are used, walking the wrapping chain the way
// errors.As walks it (the relation fmt.Errorf("%w") establishes):
//
//	*StatusError -> "HTTP <code>" (the URL never appears)
//	*url.Error -> SafeError(cause) (the URL lives in url.Error.URL)
//	other -> err.Error
//
// A *url.Error wrapping a *StatusError renders as the status code; a transport
// cause nested in wrappers keeps its own text, so "dial tcp...: connection
// refused" stays diagnosable. Context added by a wrapper is NOT reproduced —
// the caller supplies its own prefix instead
// (e.g. fmt.Errorf("webapi: token exchange: %s", httpx.SafeError(err))).
//
// Precedence and scope of identification (deliberate): the type is looked up in
// the WHOLE wrapping chain — that is what errors.As does — and a *StatusError
// found anywhere wins over an enclosing *url.Error, whatever the nesting depth.
// An outermost-only type switch would be weaker, not stricter: for
// fmt.Errorf("token exchange: %w", statusError) it would fall through to
// err.Error and print the URL again. The chain is only ever consulted one
// level at a time for rendering (url.Error contributes nothing but its cause),
// so no URL is rendered at any depth.
//
// It is a pure rendering function: err is not modified, no sentinel is
// rewritten, and errors.Is/errors.As keep working on the original value.
func SafeError(err error) string {
	if err == nil {
		return ""
	}

	// errors.As walks the wrapping chain established by fmt.Errorf("%w") and
	// by *url.Error.Unwrap.
	var status *StatusError
	if errors.As(err, &status) {
		return fmt.Sprintf("HTTP %d", status.Code)
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		// The URL is urlErr.URL; the cause is re-rendered by the same rules
		// rather than trusted blindly, because the cause can itself be a
		// wrapper around another URL carrier.
		return SafeError(urlErr.Err)
	}
	return err.Error()
}
