// Package core is the application orchestration layer. It owns the state one
// run needs and the order the subsystem packages are driven in: internal/httpx
// carries the transport, internal/webapi speaks the website protocol,
// internal/auth persists Galaxy credentials, internal/galaxy speaks the Galaxy
// content API and internal/catalog assembles product lists.
//
// It orchestrates and renders nothing, and it never exits the process. The
// front end (internal/cli) keeps the command line, the display and the exit
// code; everything this package needs from the user travels through the Console
// interface, so it never touches a terminal itself and stays testable with a
// double.
package core
