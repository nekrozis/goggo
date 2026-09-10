//go:build windows

package core

import (
	"fmt"
	"syscall"
	"unsafe"
)

// kernel32 and procGetDiskFreeSpaceEx exist only in this build-tagged file: Go
// has no cross-platform disk-space API, and the stdlib syscall package does not
// wrap GetDiskFreeSpaceExW on Windows.
var (
	kernel32               = syscall.NewLazyDLL("kernel32.dll")
	procGetDiskFreeSpaceEx = kernel32.NewProc("GetDiskFreeSpaceExW")
)

// freeSpaceAvailable returns the bytes available to the caller on the volume
// that contains path (GetDiskFreeSpaceExW's user-available counter, the one
// boost::filesystem::space reports as available).
func freeSpaceAvailable(path string) (uint64, error) {
	ptr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, fmt.Errorf("disk space: %w", err)
	}
	var available uint64
	r1, _, callErr := procGetDiskFreeSpaceEx.Call(
		uintptr(unsafe.Pointer(ptr)),
		uintptr(unsafe.Pointer(&available)),
		0,
		0,
	)
	if r1 == 0 {
		return 0, fmt.Errorf("disk space: %v", callErr)
	}
	return available, nil
}
