package webapi

import (
	"time"

	"github.com/nekrozis/goggo/internal/httpx"
)

// RetryPolicyFor maps the retry configuration the website endpoints need onto a
// transport policy.
//
// The C++ getResponse uses max_retries = min(3, iRetries) (website.cpp:33), so
// the number of attempts is that value plus one. Wait is forwarded unchanged;
// its unit is the caller's business (the C++ option is handed to usleep, i.e.
// microseconds — see the S12 audit note on --wait).
//
// ShouldRetry is deliberately left zero so httpx.New keeps ownership of the
// default predicate. This function maps the website retry rule only; it is not
// a general builder for httpx configuration.
func RetryPolicyFor(retries int, wait time.Duration) httpx.RetryPolicy {
	switch {
	case retries < 0:
		retries = 0
	case retries > 3:
		retries = 3
	}
	return httpx.RetryPolicy{
		MaxAttempts: retries + 1,
		Wait:        wait,
	}
}
