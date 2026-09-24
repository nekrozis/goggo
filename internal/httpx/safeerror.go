package httpx

import (
	"errors"
	"fmt"
	"net/url"
)

// SafeError renders err without the request URL.
//
// Transport errors embed the URL they were sent to, which would leak a
// credential into logs on an endpoint whose query string carries one (the OAuth
// token endpoint takes client_secret, code and refresh_token). Only known-safe
// renderings are used, found by walking the wrapping chain as errors.As does:
//
//	*StatusError -> "HTTP <code>"; *url.Error -> SafeError(cause); other -> err.Error
//
// The lookup covers the whole chain, so a *StatusError anywhere wins over an
// enclosing *url.Error; an outermost-only check would print the URL again.
// err is never modified, and errors.Is/errors.As keep working on it.
func SafeError(err error) string {
	if err == nil {
		return ""
	}

	// errors.AsType walks the wrapping chain established by fmt.Errorf("%w")
	// and by *url.Error.Unwrap.
	if status, ok := errors.AsType[*StatusError](err); ok {
		return fmt.Sprintf("HTTP %d", status.Code)
	}
	if urlErr, ok := errors.AsType[*url.Error](err); ok {
		// The URL is urlErr.URL; the cause is re-rendered by the same rules
		// rather than trusted blindly, because the cause can itself be a
		// wrapper around another URL carrier.
		return SafeError(urlErr.Err)
	}
	return err.Error()
}
