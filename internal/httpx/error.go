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
