// Package zipx provides the low-level ZIP structure parsing used by goggo,
// ported from src/ziputil.cpp and include/ziputil.h of LGOGDownloader
// (WTFPL; pinned reference under /reference).
//
// Design contract (review locks, S08a):
//   - archive/zip is a TEST FIXTURE GENERATOR only, never an implementation
//     oracle; ZIP64, extended-timestamp and unix-extra behaviour is covered
//     by hand-built binary fixtures.
//   - "not found" lookups return (offset, ok); the C++ -1 sentinel is not
//     spread through the API.
//   - A single set of little-endian reader primitives over io.Reader exists;
//     []byte callers wrap with bytes.NewReader.
//   - ZIP64 sentinel fields (0xFFFFFFFF) are resolved per field, in spec
//     order, consuming a uint64 only for fields that need it.
//   - Parsers must never panic on malformed/truncated input; every read is
//     bounds-checked and surfaces an error.
//   - fs.FileMode mappings express the permission bits the original project
//     uses; they are NOT a lossless POSIX st_mode representation.
//   - ParseCDEntry/ParseLocalEntry parse ONE record already positioned at its
//     start. They do NOT locate the central directory, cross-check the whole
//     archive for consistency, or validate entry ordering; callers such as
//     the S08b extractor drive those concerns.
//   - ExtractStream expects the reader to begin at a local-file header and
//     consumes that header before copying/decompressing the entry data. It
//     does NOT locate or validate a central-directory record, and it never
//     truncates to the declared compressed size: deflate runs until its
//     stream ends and stored data is copied to EOF (matching the C++ source).
//     Trailing bytes after the entry data are therefore not an error, and
//     callers must not rely on the reader position after extraction (deflate
//     may buffer ahead).
//   - Error policy: malformed external input surfaces an error; violations of
//     an internal API contract (e.g. a nil io.Reader) are programming errors
//     and are not recovered here. No function in this package adds an
//     inconsistent nil-guard.
//   - Integrity boundary: ExtractStream is raw extraction semantics only. It
//     performs NO size, CRC or hash validation, and an early EOF on the
//     stored path is not an error. Size/hash/integrity checks belong to the
//     consuming depot/chunk layer, never back inside ExtractStream.
package zipx
