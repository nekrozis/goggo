// Package zipx parses and extracts ZIP structures at the record level: the
// central-directory and local-file headers, the ZIP64 extensions, the extra
// fields and the raw entry data.
//
// The package reads from io.Reader primitives and never panics on malformed or
// truncated input: every read is bounds-checked and surfaces an error. A
// "not found" lookup returns (offset, ok) rather than a sentinel.
//
// ExtractStream is raw extraction only: it performs no size, CRC or hash
// validation, and an early EOF on the stored path is not an error. Integrity
// checks belong to the consuming depot/chunk layer, never inside this package.
package zipx
