// Package core is the application orchestration layer: the port of the C++
// class Downloader (include/downloader.h, src/downloader.cpp).
//
// It owns the state one run needs and the order the subsystem packages are
// driven in: internal/httpx carries the transport, internal/webapi speaks the
// website protocol, internal/auth persists Galaxy credentials, internal/galaxy
// speaks the Galaxy content API and internal/catalog assembles product lists.
// Nothing here re-implements protocol, filtering or rendering.
//
// The front end (internal/cli) keeps the command line, the rendering and the
// exit code. Everything it needs from the user travels through the Console
// interface, so this package never touches a terminal itself and stays testable
// with a double.
//
// Deliberate omissions from the C++ class, each waiting for its first real
// consumer: the public curl handle, Timer, progressbar, TimeAndSize, the resume
// position and the retry counters. Also not ported yet: the report file, the
// download engine (download/repair/status/orphans), the game-details cache and
// the cloud-save commands.
package core
