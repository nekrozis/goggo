// Package core is the application orchestration layer. It owns the state one
// run needs and the order the subsystem packages are driven in.
//
// It orchestrates and renders nothing, and it never exits the process: the
// front end (internal/cli) keeps the command line, the display and the exit
// code, and everything this package needs from the user travels through the
// Console interface, so it never touches a terminal itself.
package core
