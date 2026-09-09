package zipx

import "io/fs"

// POSIX permission/type bits used by ZIP external attributes (high 16 bits
// of the central directory entry). Values follow the standard octal layout.
const (
	unixIRUSR = uint16(0o400)
	unixIWUSR = uint16(0o200)
	unixIXUSR = uint16(0o100)
	unixIRGRP = uint16(0o040)
	unixIWGRP = uint16(0o020)
	unixIXGRP = uint16(0o010)
	unixIROTH = uint16(0o004)
	unixIWOTH = uint16(0o002)
	unixIXOTH = uint16(0o001)

	unixIFMT  = uint16(0o170000)
	unixIFLNK = uint16(0o120000)
)

// fileModeFromUnixMode maps the nine POSIX permission bits to fs.FileMode
// (ziputil.cpp:604-629). It expresses exactly what the original program uses
// and is NOT a lossless representation of POSIX st_mode: uid/gid and special
// bits have no fs.FileMode equivalent and are dropped.
func fileModeFromUnixMode(mode uint16) fs.FileMode {
	var fm fs.FileMode
	if mode&unixIRUSR != 0 {
		fm |= 0o400
	}
	if mode&unixIWUSR != 0 {
		fm |= 0o200
	}
	if mode&unixIXUSR != 0 {
		fm |= 0o100
	}
	if mode&unixIRGRP != 0 {
		fm |= 0o040
	}
	if mode&unixIWGRP != 0 {
		fm |= 0o020
	}
	if mode&unixIXGRP != 0 {
		fm |= 0o010
	}
	if mode&unixIROTH != 0 {
		fm |= 0o004
	}
	if mode&unixIWOTH != 0 {
		fm |= 0o002
	}
	if mode&unixIXOTH != 0 {
		fm |= 0o001
	}
	return fm
}

// isSymlink reports whether the Unix mode bits identify a symlink
// (ziputil.cpp:631-635).
func isSymlink(mode uint16) bool {
	return mode&unixIFMT == unixIFLNK
}

// unixMode derives the Unix mode word (high 16 bits) of a central directory
// entry's external attributes.
func unixMode(entry CDEntry) uint16 {
	return uint16(entry.ExternalAttr >> 16)
}
