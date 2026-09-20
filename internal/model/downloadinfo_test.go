package model

import (
	"sync"
	"testing"
)

func TestDownloadInfoDefaults(t *testing.T) {
	d := NewDownloadInfo()
	if got := d.GetStatus(); got != DLStatusNotStarted {
		t.Errorf("initial status = %d, want DLStatusNotStarted", got)
	}
	if got := d.GetFilename(); got != "" {
		t.Errorf("initial filename = %q, want empty", got)
	}
	if got := d.GetProgressInfo(); got != (ProgressInfo{}) {
		t.Errorf("initial progress = %+v, want zero", got)
	}
}

func TestDownloadInfoAccessors(t *testing.T) {
	d := NewDownloadInfo()
	d.SetFilename("game/setup.exe")
	d.SetStatus(DLStatusRunning)
	d.SetProgressInfo(ProgressInfo{DLNow: 10, DLTotal: 100, Rate: 5.0, RateAvg: 4.0})

	if got := d.GetFilename(); got != "game/setup.exe" {
		t.Errorf("filename = %q", got)
	}
	if got := d.GetStatus(); got != DLStatusRunning {
		t.Errorf("status = %d", got)
	}
	if got := d.GetProgressInfo(); got.DLNow != 10 || got.DLTotal != 100 || got.Rate != 5.0 {
		t.Errorf("progress = %+v", got)
	}
}

// TestDownloadInfoConcurrentAccess is a smoke test for the lock discipline, not
// an accessor test: it hammers one value from nine goroutines and asserts the
// final status. With the race detector unavailable in this build (no cgo) it can
// only catch a panic or a deadlock; the accessor contracts live in the tests
// above.
func TestDownloadInfoConcurrentAccess(t *testing.T) {
	d := NewDownloadInfo()
	d.SetFilename("start")
	d.SetStatus(DLStatusRunning)
	d.SetProgressInfo(ProgressInfo{DLTotal: 1000})

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				d.GetFilename()
				d.GetStatus()
				p := d.GetProgressInfo()
				_ = p.DLNow
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 500; j++ {
			d.SetProgressInfo(ProgressInfo{DLNow: int64(j), DLTotal: 1000})
			d.SetStatus(DLStatusRunning)
		}
	}()
	wg.Wait()

	d.SetStatus(DLStatusFinished)
	if got := d.GetStatus(); got != DLStatusFinished {
		t.Errorf("final status = %d, want DLStatusFinished", got)
	}
}
