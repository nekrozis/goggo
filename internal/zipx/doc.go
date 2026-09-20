// Package zipx parses and extracts ZIP structures at the record level: the
// central-directory and local-file headers, the ZIP64 extensions, the extra fields
// and the raw entry data.
//
// It reads from io.Reader primitives and never panics on malformed or truncated
// input: every read is bounds-checked and surfaces an error. ExtractStream is raw
// extraction only — no size, CRC or hash validation, and an early EOF on the stored
// path is not an error; integrity checks belong to the consuming depot/chunk layer.
package zipx
