package webapi

import (
	"testing"
	"time"
)

// TestRetryPolicyFor locks the website retry rule: at most min(3, retries)
// additional attempts, the wait forwarded unchanged, and the retry predicate
// left to httpx.
func TestRetryPolicyFor(t *testing.T) {
	cases := []struct {
		name    string
		retries int
		wantMax int
	}{
		{"negative clamps to zero", -1, 1},
		{"zero", 0, 1},
		{"one", 1, 2},
		{"three", 3, 4},
		{"above the cap", 10, 4},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := RetryPolicyFor(c.retries, 25*time.Millisecond)
			if p.MaxAttempts != c.wantMax {
				t.Errorf("MaxAttempts = %d, want %d", p.MaxAttempts, c.wantMax)
			}
			if p.Wait != 25*time.Millisecond {
				t.Errorf("Wait = %v, want it forwarded unchanged", p.Wait)
			}
			if p.ShouldRetry != nil {
				t.Error("ShouldRetry must stay nil so httpx.New owns the default predicate")
			}
		})
	}
}
