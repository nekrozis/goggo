// Package cli is the command-line front end: it parses a command line into a
// config.Config, drives the orchestration in internal/core and renders the
// output. All input and output goes through the io.Reader/Writer pair passed to
// Run, so nothing touches the real terminal and tests can drive the whole front
// end.
//
// Exit codes: 0 success, 1 operational failure, 2 usage failure, 130
// interrupted (run.go owns the mapping).
package cli
