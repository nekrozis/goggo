// Package util hosts the pure helper clusters shared across goggo: string splitting
// and stripping, option-value parsing, size/rate/ETA formatting, JSON file reading,
// per-user path resolution and string replacement.
//
// A new helper must belong to one of these clusters; anything with domain semantics
// belongs in its own package instead.
package util
