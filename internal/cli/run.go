package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/nekrozis/goggo/internal/config"
	"github.com/nekrozis/goggo/internal/util"
)

// newConfig resolves the XDG roots (the single entry point for path
// resolution) and builds the defaults from them.
func newConfig() (config.Config, error) {
	configHome, err := util.ConfigHome()
	if err != nil {
		return config.Config{}, err
	}
	cacheHome, err := util.CacheHome()
	if err != nil {
		return config.Config{}, err
	}
	return config.NewConfig(configHome, cacheHome), nil
}

// Run executes one command line and returns the process exit code.
//
// It performs no process-level work: all input and output goes through the
// streams it is given, so the whole front end is testable and nothing can reach
// the real terminal. Exit codes are 0 for success and 1 for any failure, as in
// the C++ front end.
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	cfg, err := newConfig()
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	inv, err := Parse(args, cfg)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}

	switch {
	case inv.Help:
		fmt.Fprintln(stdout, config.VersionString)
		usage(stdout)
		return 0
	case inv.Version:
		// Identity first, then the upstream release this port tracks: the
		// compatibility baseline is metadata, never presented as our version.
		fmt.Fprintln(stdout, config.VersionString)
		fmt.Fprintf(stdout, "%s compatibility: %s\n", config.UpstreamName, config.UpstreamCompatibilityVersion)
		return 0
	case inv.Unsupported != "":
		// Recognised option, but this build does not implement it: fail loudly
		// rather than report a success that never happened.
		fmt.Fprintf(stderr, "Error: %s is not implemented in this build\n", inv.Unsupported)
		return 1
	}

	ui := newConsole(stdin, stdout, stderr)
	ctx := context.Background()

	// --check-login-status answers before any login is attempted
	// (main.cpp:686-698).
	if inv.CheckLoginStatus {
		s, err := Open(ctx, inv.Config, ui, false)
		if err != nil {
			fmt.Fprintf(stderr, "Error: %v\n", err)
			return 1
		}
		if s.LoggedIn {
			fmt.Fprintln(stdout, "Login status: Logged in")
			return 0
		}
		fmt.Fprintln(stdout, "Login status: Not logged in")
		return 1
	}

	// --logout clears the local login state and stops. It sits after the check
	// above — a query outranks a mutation, so both flags together answer the
	// query and remove nothing — and before the session below, because it must
	// never open one: a Session flushes its cookie jar on Close, which would
	// write the removed cookie file straight back.
	if inv.Logout {
		if err := logout(inv.Config, stdout); err != nil {
			fmt.Fprintf(stderr, "Error: %v\n", err)
			return 1
		}
		return 0
	}

	s, err := Open(ctx, inv.Config, ui, true)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	defer func() { _ = s.Close() }()

	if inv.List {
		if err := renderList(ctx, s, inv, stdout); err != nil {
			fmt.Fprintf(stderr, "Error: %v\n", err)
			return 1
		}
	}
	return 0
}
