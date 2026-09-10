//go:build !windows

package core

import (
	"fmt"
	"syscall"
)

// freeSpaceAvailable returns the bytes available to the caller on the volume
// that contains path. Bavail is the unprivileged free block count — the counter
// boost::filesystem::space reports as available.
func freeSpaceAvailable(path string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, fmt.Errorf("disk space: %w", err)
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}
