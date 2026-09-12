//go:build aix || darwin || dragonfly || freebsd || linux || windows

package core

import "testing"

// TestFreeSpaceAvailableReportsTheVolume locks the positive path the free-space
// gate depends on: a real directory resolves to a non-zero available count.
// The test carries the same build constraint as the implementations, so the
// stub platform never runs it.
func TestFreeSpaceAvailableReportsTheVolume(t *testing.T) {
	available, err := freeSpaceAvailable(t.TempDir())
	if err != nil {
		t.Fatalf("freeSpaceAvailable(temp dir) = %v, want the volume's free bytes", err)
	}
	if available == 0 {
		t.Errorf("available = 0, want a non-zero count for a writable volume")
	}
}

// TestFreeSpaceAvailableRejectsUnusablePath locks the error branch both
// implementations share: a path the OS cannot encode is reported, not turned
// into a zero-byte answer. The NUL path is the environment-independent way to
// fail the encode on windows (UTF16PtrFromString) and on unix (the syscall
// wrappers reject it before the call).
func TestFreeSpaceAvailableRejectsUnusablePath(t *testing.T) {
	if _, err := freeSpaceAvailable("x\x00y"); err == nil {
		t.Error("freeSpaceAvailable(NUL path) = nil error, want a rejection")
	}
}
