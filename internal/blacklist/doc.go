// Package blacklist filters planned download paths against a user-maintained
// list of regular expressions.
//
// It is a runtime input of plan building, not global configuration state: the
// caller loads one from a file (or builds it from lines) and hands it to the
// plan builder, which drops every planned file the list matches.
//
// The file format is one entry per line: a flag prefix, a space, then the
// expression. The only meaningful flag is 'R' (regex), 'p' is a no-op, and a
// line without 'R' is reported and skipped. Empty lines and '#' comments are
// skipped. Matching is an unanchored search, first hit wins.
package blacklist
