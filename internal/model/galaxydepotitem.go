package model

// GalaxyDepotItemChunk mirrors struct galaxyDepotItemChunk
// (include/galaxyapi.h:22-30): one chunk of a depot entry, with its byte range
// inside the compressed stream and inside the uncompressed file.
//
// Fields are ordered to minimise padding: the strings (16B each), then the 8B
// sizes and offsets.
//
// The names follow the original: the bare name is the UNCOMPRESSED side
// (md5_uncompressed, size, offset_uncompressed) and the Compressed prefix is the
// compressed one (md5_compressed, compressedSize, offset_compressed). The C++
// struct uses uintmax_t; Go uses uint64 and the reader rejects values that do not
// fit (see uint64Value in internal/galaxy).
type GalaxyDepotItemChunk struct {
	CompressedMD5 string
	MD5           string

	CompressedSize   uint64
	Size             uint64
	CompressedOffset uint64
	Offset           uint64
}

// GalaxyDepotItem mirrors struct galaxyDepotItem (include/galaxyapi.h:32-45):
// one depot entry — a file of the build, or the synthetic small-files container.
//
// Fields are ordered to minimise padding: the slice (24B), the strings (16B
// each), the 8B totals and SFC range, then the bools.
//
// Differences from the C++ struct (intentional): SFCOffset and SFCSize are zero
// unless IsInSFC is set, where upstream leaves them UNINITIALISED and assigns
// them only inside the sfcRef branch (reading them there is undefined
// behaviour). ProductID is left empty by the depot reader and stamped by its
// caller, exactly as upstream does (downloader.cpp:3977-3986).
type GalaxyDepotItem struct {
	Chunks []GalaxyDepotItemChunk

	Path      string
	MD5       string
	ProductID string

	TotalCompressedSize uint64
	TotalSize           uint64
	SFCOffset           uint64
	SFCSize             uint64

	IsDependency          bool
	IsSmallFilesContainer bool
	IsInSFC               bool
}
