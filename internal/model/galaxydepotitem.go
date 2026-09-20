package model

// GalaxyDepotItemChunk is one chunk of a depot entry: its byte range inside the
// compressed stream and inside the uncompressed file.
//
// The bare names are the UNCOMPRESSED side (MD5, Size, Offset) and the Compressed
// prefix is the compressed one (CompressedMD5, CompressedSize, CompressedOffset).
type GalaxyDepotItemChunk struct {
	CompressedMD5 string
	MD5           string

	CompressedSize   uint64
	Size             uint64
	CompressedOffset uint64
	Offset           uint64
}

// GalaxyDepotItem is one depot entry: a file of the build, or the synthetic
// small-files container.
//
// SFCOffset and SFCSize are zero unless IsInSFC is set. ProductID is left empty
// by the depot reader and stamped by its caller.
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
