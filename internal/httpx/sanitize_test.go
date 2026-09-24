package httpx

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
)

// The signed download URLs of both chains carry the session token in the query
// string, so these are the shapes the boundary has to survive: the website
// chain's downlink and the Galaxy CDN chunk URL.
const (
	testSignedDownload = "https://cdn.gog.com/content-system/v2/chunks/abc/def?" +
		"path=%2Fgame%2Fdata.bin&token=one-time-token&access_token=long-lived-token"
	testCDNChunk = "https://cdn.gog.com/content-system/v2/chunks/abc?" +
		"access_token=long-lived-token&path=%2Fgame%2Fdata.bin"
)

// TestSanitizeURLStripsCredentialParams locks the parameter list and, just as
// importantly, what is NOT on it: a diagnostic parameter removed "to be safe"
// would cost the message its usefulness without protecting anything.
func TestSanitizeURLStripsCredentialParams(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "the website downlink's token pair",
			in:   testSignedDownload,
			want: "https://cdn.gog.com/content-system/v2/chunks/abc/def?path=%2Fgame%2Fdata.bin",
		},
		{
			name: "the CDN chunk's access token, path kept in place",
			in:   testCDNChunk,
			want: "https://cdn.gog.com/content-system/v2/chunks/abc?path=%2Fgame%2Fdata.bin",
		},
		{
			name: "every credential name at once",
			in:   "https://x/y?a=1&access_token=T&refresh_token=R&token=K&code=C&client_secret=S&b=2",
			want: "https://x/y?a=1&b=2",
		},
		{
			name: "a near miss is NOT a credential",
			in:   "https://x/y?token_x=keep&access_token_expires=keep&codes=keep&client_id=keep",
			want: "https://x/y?token_x=keep&access_token_expires=keep&codes=keep&client_id=keep",
		},
		{
			name: "every occurrence of a repeated parameter goes",
			in:   "https://x/y?token=A&path=p&token=B",
			want: "https://x/y?path=p",
		},
		{
			name: "parameter order and encoding survive byte for byte",
			in:   "https://x/y?z=%2F%2F&a=b%20c&token=T&m=n",
			want: "https://x/y?z=%2F%2F&a=b%20c&m=n",
		},
		{
			name: "a fragment is not part of the query",
			in:   "https://x/y?token=T&a=b#section-1",
			want: "https://x/y?a=b#section-1",
		},
		{
			name: "a bare credential key with no value",
			in:   "https://x/y?token&a=b",
			want: "https://x/y?a=b",
		},
		{
			name: "no query at all",
			in:   "https://x/y/z",
			want: "https://x/y/z",
		},
		{
			name: "an empty query",
			in:   "https://x/y?",
			want: "https://x/y?",
		},
		{
			name: "a query with nothing to remove",
			in:   "https://x/y?a=b&c=d",
			want: "https://x/y?a=b&c=d",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := SanitizeURL(c.in); got != c.want {
				t.Errorf("SanitizeURL(%q)\n got %q\nwant %q", c.in, got, c.want)
			}
		})
	}
}

// TestSanitizeURLLeavesNothingCredentialShaped is the property the boundary
// exists for, stated directly: whatever survives must not carry a token value.
func TestSanitizeURLLeavesNothingCredentialShaped(t *testing.T) {
	for _, in := range []string{testSignedDownload, testCDNChunk, "https://x/y?token=T"} {
		got := SanitizeURL(in)
		if strings.Contains(got, "long-lived-token") || strings.Contains(got, "one-time-token") {
			t.Errorf("SanitizeURL(%q) = %q, want no token value left", in, got)
		}
	}
}

// TestSanitizeErrorPreservesTheChain locks the constraint the retry and resume
// logic depends on: the URL is replaced, the error is not rebuilt. A rebuilt
// error would break errors.Is on the context sentinels (the retry policy asks
// whether the failure was a cancellation) and Timeout.
func TestSanitizeErrorPreservesTheChain(t *testing.T) {
	cause := context.DeadlineExceeded
	orig := &url.Error{Op: "Get", URL: testCDNChunk, Err: cause}

	got := SanitizeError(orig)

	if !errors.Is(got, context.DeadlineExceeded) {
		t.Errorf("errors.Is(%v, DeadlineExceeded) = false, want the chain intact", got)
	}
	var urlErr *url.Error
	if !errors.As(got, &urlErr) {
		t.Fatalf("errors.As(%v, *url.Error) = false, want the type intact", got)
	}
	if !urlErr.Timeout() {
		t.Error("Timeout() = false after sanitising, want the deadline cause to answer")
	}
	if urlErr.Op != "Get" {
		t.Errorf("Op = %q, want it preserved", urlErr.Op)
	}
	if urlErr.Unwrap() != cause {
		t.Errorf("Unwrap() = %v, want the original cause", urlErr.Unwrap())
	}
	if urlErr.URL != "https://cdn.gog.com/content-system/v2/chunks/abc?path=%2Fgame%2Fdata.bin" {
		t.Errorf("URL = %q, want the credential parameter gone and the path kept", urlErr.URL)
	}
}

// TestSanitizeErrorKeepsAWrappingMessage locks the ordering the boundary
// requires: sanitise first, wrap after. The wrapper then carries a message that
// is already clean.
func TestSanitizeErrorKeepsAWrappingMessage(t *testing.T) {
	clean := SanitizeError(&url.Error{Op: "Get", URL: testCDNChunk, Err: errors.New("broken pipe")})
	wrapped := fmt.Errorf("transfer: chunk 3: %w", clean)

	if !strings.Contains(wrapped.Error(), "transfer: chunk 3") {
		t.Errorf("error = %q, want the wrapping message kept", wrapped)
	}
	if strings.Contains(wrapped.Error(), "long-lived-token") {
		t.Errorf("error = %q, want the token gone", wrapped)
	}
	if !errors.Is(wrapped, clean) {
		t.Error("errors.Is(wrapped, clean) = false, want the chain intact")
	}
}

// TestSanitizeErrorCannotRepairAnAlreadyWrappedError pins the limitation that
// makes this a boundary rather than a cleanup: fmt.Errorf formats its message at
// wrap time, so a wrapper built around a raw *url.Error keeps the raw URL in its
// text even though the inner value is edited afterwards. The test states the
// behaviour so nobody mistakes SanitizeError for a last line of defence — every
// call site must apply it where the error is created.
func TestSanitizeErrorCannotRepairAnAlreadyWrappedError(t *testing.T) {
	raw := &url.Error{Op: "Get", URL: testCDNChunk, Err: errors.New("broken pipe")}
	wrapped := fmt.Errorf("transfer: chunk 3: %w", raw)

	SanitizeError(wrapped)

	if strings.Contains(raw.URL, "long-lived-token") {
		t.Errorf("inner URL = %q, want it sanitised", raw.URL)
	}
	if !strings.Contains(wrapped.Error(), "long-lived-token") {
		t.Error("the wrapper's text is already formatted; this test documents that sanitising late cannot fix it")
	}
}

// TestSanitizeErrorIsIdempotent locks the second call: it must not rewrap, must
// not change the type, and must leave an already-clean URL alone.
func TestSanitizeErrorIsIdempotent(t *testing.T) {
	orig := &url.Error{Op: "Get", URL: testCDNChunk, Err: errors.New("broken pipe")}

	once := SanitizeError(orig)
	twice := SanitizeError(once)

	if twice != once {
		t.Errorf("second SanitizeError returned a different error (%v vs %v), want the same one", twice, once)
	}
	if once.Error() != twice.Error() {
		t.Errorf("message changed on the second pass: %q then %q", once, twice)
	}
	if n := strings.Count(twice.Error(), "Get "); n != 1 {
		t.Errorf("error = %q, want exactly one op prefix (no double wrapping), got %d", twice, n)
	}
}

// TestSanitizeErrorPassesAStatusErrorThrough locks that the two boundaries do
// not compound: NewStatusError has already sanitised its URL, and SanitizeError
// must hand the same error back untouched.
func TestSanitizeErrorPassesAStatusErrorThrough(t *testing.T) {
	status := NewStatusError("GET", testCDNChunk, 403)

	if got := SanitizeError(status); got != error(status) {
		t.Errorf("SanitizeError(%v) = %v, want the same error", status, got)
	}
}

// TestNewStatusErrorSanitizes locks the constructor as the single entry point:
// no production site builds a StatusError literal, so a signed URL cannot reach
// an error message through this type.
func TestNewStatusErrorSanitizes(t *testing.T) {
	err := NewStatusError("GET", testSignedDownload, 404)

	if strings.Contains(err.Error(), "long-lived-token") || strings.Contains(err.Error(), "one-time-token") {
		t.Errorf("Error() = %q, want no token in the message", err)
	}
	if !strings.Contains(err.Error(), "HTTP 404") || !strings.Contains(err.Error(), "cdn.gog.com") {
		t.Errorf("Error() = %q, want the method, URL and code kept", err)
	}
}

// TestSanitizeErrorLeavesAPlainErrorAlone locks that the boundary is not a
// general error rewriter: anything that is not a URL carrier comes back as it
// was, so callers cannot lose a sentinel they compare with errors.Is.
func TestSanitizeErrorLeavesAPlainErrorAlone(t *testing.T) {
	sentinel := errors.New("some failure")

	if got := SanitizeError(sentinel); got != sentinel {
		t.Errorf("SanitizeError(%v) = %v, want the same error", sentinel, got)
	}
	if got := SanitizeError(nil); got != nil {
		t.Errorf("SanitizeError(nil) = %v, want nil", got)
	}
}

// TestSanitizeErrorCoversATransportFailure is the end-to-end shape of the leak
// this boundary closes: a real client against a closed port reports a *url.Error
// carrying the request URL, and the request URL is a signed one.
func TestSanitizeErrorCoversATransportFailure(t *testing.T) {
	c, err := New(testConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// A port nothing listens on: the transport fails before any response.
	target := "http://127.0.0.1:1/x?access_token=" + "long-lived-token"

	_, err = c.GetBytes(context.Background(), target)
	if err == nil {
		t.Fatal("GetBytes to a closed port: want a transport failure")
	}
	if strings.Contains(err.Error(), "long-lived-token") {
		t.Errorf("error = %q, want the token stripped by the boundary", err)
	}
	if _, ok := errors.AsType[*url.Error](err); !ok {
		t.Fatalf("error = %T, want a *url.Error to survive for the retry policy", err)
	}
	if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		// Not a failure: the point is only that the chain still answers. A
		// refused connection is neither, and the retry policy treats it as
		// retryable.
		if !DefaultShouldRetry(nil, err) {
			t.Errorf("DefaultShouldRetry(nil, %v) = false, want a refused connection retried", err)
		}
	}
}
