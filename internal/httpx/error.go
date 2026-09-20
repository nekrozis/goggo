package httpx

import "fmt"

// StatusError is returned when the server answers with an HTTP status >= 400
// and the response body was fully consumed (GetBytes/GetBytesWithRetry).
// The raw transport layer (Do/Get) returns *http.Response instead so that
// retry and streaming decisions stay with the caller.
type StatusError struct {
	Method string
	URL    string
	Code   int
}

// Error implements the error interface, e.g. "GET https://…: HTTP 404".
func (e *StatusError) Error() string {
	return fmt.Sprintf("%s %s: HTTP %d", e.Method, e.URL, e.Code)
}

// NewStatusError is the single constructor for a status failure, and the
// boundary where a response's URL loses its credential parameters. Every
// production site builds its StatusError through it: a bare composite literal
// would put a signed URL — the website chain's downlink and the Galaxy CDN chunk
// URL both carry the session's access token — straight into an error message
// that reaches the terminal and any log collecting it.
func NewStatusError(method, rawURL string, code int) *StatusError {
	return &StatusError{Method: method, URL: SanitizeURL(rawURL), Code: code}
}
