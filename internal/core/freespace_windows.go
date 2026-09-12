//go:build windows

package core

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// freeSpaceAvailable returns the bytes available to the caller on the volume
// that contains path (GetDiskFreeSpaceExW's user-available counter, the one
// boost::filesystem::space reports as available). The typed wrapper replaces
// the NewLazyDLL/NewProc/unsafe pair this file used to carry by hand; the
// total-size and total-free out parameters are optional to the API, so nil
// stands in for both.
func freeSpaceAvailable(path string) (uint64, error) {
	ptr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, fmt.Errorf("disk space: %w", err)
	}
	var available uint64
	if err := windows.GetDiskFreeSpaceEx(ptr, &available, nil, nil); err != nil {
		return 0, fmt.Errorf("disk space: %w", err)
	}
	return available, nil
}
