package transfer

import "time"

// Options is the execution tuning one run accepts.
//
// The durations are explicit so the caller converts from the configuration's
// int fields; transfer never reads config. The upstream units are milliseconds
// for both (main.cpp:292 --wait, main.cpp:313 --progress-interval), so the
// front end maps with time.Duration(cfg.Wait)*time.Millisecond and
// time.Duration(cfg.ProgressInterval)*time.Millisecond.
type Options struct {
	Workers          int
	Retries          int
	Wait             time.Duration
	ProgressInterval time.Duration
}
