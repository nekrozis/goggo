//go:build aix || darwin || dragonfly || freebsd || linux

package core

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// freeSpaceAvailable returns the bytes available to the caller on the volume
// that contains path. Bavail is the unprivileged free block count — the counter
// boost::filesystem::space reports as available.
//
// The build constraint names the platforms whose Statfs_t actually carries the
// Bavail/Bsize pair. The previous !windows tag also claimed openbsd (whose
// Statfs_t names them F_bavail/F_bsize), netbsd and solaris (which have no
// syscall.Statfs/Statfs_t at all) and the non-unix ports, so those targets
// never compiled. Everything outside this set builds through
// freespace_unsupported.go instead (review 2026-09-12).
func freeSpaceAvailable(path string) (uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, fmt.Errorf("disk space: %w", err)
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}
