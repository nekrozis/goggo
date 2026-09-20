//go:build !windows && !(aix || darwin || dragonfly || freebsd || linux)

package core

import (
	"fmt"
	"runtime"
)

// freeSpaceAvailable reports that the free-space gate has no implementation on
// this platform: openbsd names the counters F_bavail/F_bsize, netbsd and
// solaris have no Statfs/Statfs_t beyond the stdlib, and plan9, js and wasip1
// are not unix ports at all. Refusing the check is deliberate — the gate is an
// option, so the failure is loud and local rather than a build break, and a
// platform without a counter must not pretend the disk has room.
func freeSpaceAvailable(string) (uint64, error) {
	return 0, fmt.Errorf("disk space: unsupported platform %s", runtime.GOOS)
}
