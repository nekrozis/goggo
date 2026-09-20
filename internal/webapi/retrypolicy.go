package webapi

import (
	"time"

	"github.com/nekrozis/goggo/internal/httpx"
)

// RetryPolicyFor maps the retry configuration the website endpoints need onto a
// transport policy.
//
// Retries are capped at 3, so attempts are min(3, retries) + 1. Wait is forwarded
// unchanged: the caller owns the unit conversion (the CLI's --wait is
// milliseconds — see core.retryWait). ShouldRetry is deliberately left zero so
// httpx.New keeps ownership of the default predicate.
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
