package httpx

import (
	"errors"
	"fmt"
	"net/http"
)

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

// IsNotFound reports whether err is a 404 response error.
func IsNotFound(err error) bool {
	var se *StatusError
	return errors.As(err, &se) && se.Code == http.StatusNotFound
}

// IsForbidden reports whether err is a 403 response error.
func IsForbidden(err error) bool {
	var se *StatusError
	return errors.As(err, &se) && se.Code == http.StatusForbidden
}
