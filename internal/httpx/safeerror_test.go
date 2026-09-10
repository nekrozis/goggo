package httpx

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
)

// The queries below mirror the real OAuth requests: the token endpoint carries
// a client secret, a one-time authorization code and a refresh token. Every
// rendered message must be free of all three.
const (
	testTokenURL     = "https://auth.gog.com/token?"
	testCodeQuery    = "client_id=46899977096215655&client_secret=top-secret&grant_type=authorization_code&code=one-time-code&redirect_uri=https%3A%2F%2Fembed.gog.com%2Fon_login_success"
	testRefreshQuery = "client_id=46899977096215655&client_secret=top-secret&grant_type=refresh_token&refresh_token=long-lived-secret"
)

// TestSafeErrorStatusWinsOverOuterURLWrapper pins the documented precedence:
// identification looks up the type in the whole wrapping chain (errors.As), and
// a *StatusError wins over an enclosing *url.Error at any depth. An
// outermost-only type switch would be weaker, not stricter — for a fmt wrapper
// around a status it would fall through to err.Error() and print the URL again.
func TestSafeErrorStatusWinsOverOuterURLWrapper(t *testing.T) {
	status := &StatusError{Method: "GET", URL: testTokenURL + testRefreshQuery, Code: 403}
	nested := fmt.Errorf("auth: refresh token: %w",
		&url.Error{Op: "Get", URL: testTokenURL + testRefreshQuery, Err: status})

	got := SafeError(nested)
	if got != "HTTP 403" {
		t.Errorf("SafeError(double-wrapped status) = %q, want %q", got, "HTTP 403")
	}
	assertNoCredentials(t, got)
}

// assertNoCredentials fails when a rendered message leaks any part of the
// credential-bearing request.
func assertNoCredentials(t *testing.T, msg string) {
	t.Helper()
	for _, leak := range []string{
		"top-secret", "client_secret", "one-time-code", "long-lived-secret",
		"refresh_token", "code=", "grant_type", "auth.gog.com/token", "?client_id",
	} {
		if strings.Contains(msg, leak) {
			t.Errorf("message %q leaks %q", msg, leak)
		}
	}
}

// TestSafeErrorStatusHidesURL covers the primary case: a StatusError carries
// the full URL, and only its status code is rendered.
func TestSafeErrorStatusHidesURL(t *testing.T) {
	err := &StatusError{Method: "GET", URL: testTokenURL + testCodeQuery, Code: 400}
	got := SafeError(err)
	if got != "HTTP 400" {
		t.Errorf("SafeError = %q, want %q", got, "HTTP 400")
	}
	assertNoCredentials(t, got)
}

// TestSafeErrorURLErrorHidesURL covers both *url.Error shapes: one wrapping a
// StatusError (status wins) and one wrapping a transport failure (the cause is
// kept, the URL is not).
func TestSafeErrorURLErrorHidesURL(t *testing.T) {
	status := &StatusError{Method: "GET", URL: testTokenURL + testRefreshQuery, Code: 401}
	wrapped := &url.Error{Op: "Get", URL: testTokenURL + testRefreshQuery, Err: status}
	got := SafeError(wrapped)
	if got != "HTTP 401" {
		t.Errorf("SafeError(url.Error(StatusError)) = %q, want %q", got, "HTTP 401")
	}
	assertNoCredentials(t, got)

	transport := &url.Error{
		Op:  "Get",
		URL: testTokenURL + testCodeQuery,
		Err: errors.New("dial tcp 1.2.3.4:443: connect: connection refused"),
	}
	got = SafeError(transport)
	if !strings.Contains(got, "connection refused") {
		t.Errorf("SafeError = %q, want the transport cause preserved", got)
	}
	if !strings.Contains(got, "dial tcp") {
		t.Errorf("SafeError = %q, want the dial detail preserved", got)
	}
	assertNoCredentials(t, got)
}

// TestSafeErrorWrappedPlainAndNil covers a status nested in a fmt wrapper, a
// plain error and nil.
func TestSafeErrorWrappedPlainAndNil(t *testing.T) {
	status := &StatusError{Method: "GET", URL: testTokenURL + testCodeQuery, Code: 500}
	if got := SafeError(fmt.Errorf("token exchange: %w", status)); got != "HTTP 500" {
		t.Errorf("wrapped = %q, want %q", got, "HTTP 500")
	}
	if got := SafeError(errors.New("plain failure")); got != "plain failure" {
		t.Errorf("plain = %q", got)
	}
	if got := SafeError(nil); got != "" {
		t.Errorf("nil = %q, want the empty string", got)
	}
}

// TestSafeErrorNestedURLWrapperKeepsLooking covers the requirement that a
// *url.Error nested inside wrappers is still walked layer by layer: the URL of
// the outer wrapper must not survive just because the cause is not a
// StatusError.
func TestSafeErrorNestedURLWrapperKeepsLooking(t *testing.T) {
	inner := &url.Error{
		Op:  "Get",
		URL: testTokenURL + testRefreshQuery,
		Err: errors.New("context deadline exceeded"),
	}
	got := SafeError(fmt.Errorf("auth: refresh token: %w", inner))
	if !strings.Contains(got, "context deadline exceeded") {
		t.Errorf("SafeError = %q, want the inner cause", got)
	}
	assertNoCredentials(t, got)
}
