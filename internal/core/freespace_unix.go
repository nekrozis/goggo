//go:build aix || darwin || dragonfly || freebsd || linux

package core

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// freeSpaceAvailable returns the bytes available to the caller on the volume
// that contains path. Bavail is the unprivileged free block count.
//
// The build constraint names the platforms whose Statfs_t carries the
// Bavail/Bsize pair. Everything outside this set builds through
// freespace_unsupported.go instead.
func freeSpaceAvailable(path string) (uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, fmt.Errorf("disk space: %w", err)
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}
