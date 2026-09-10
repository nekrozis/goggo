package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/nekrozis/goggo/internal/config"
	"github.com/nekrozis/goggo/internal/core"
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
//
// The dispatch order mirrors the C++ if/else chain, which means the first match
// wins and combinations are not errors: Help, Version, an unimplemented option,
// the login-status query, the local logout and then the commands proper
// (main.cpp:686-931). The orchestration itself lives in internal/core.
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

	// The three Galaxy command arguments are resolved BEFORE any session work,
	// so a malformed argument fails without opening one. The C++ source splits
	// them at dispatch time instead (main.cpp:840-845) and reads the first token
	// without checking that the split produced any, which makes an argument of
	// "/" read past the end of an empty vector.
	showBuildsProduct, showBuildsID, err := galaxyCommandArgument(inv.GalaxyShowBuilds, "--galaxy-show-builds")
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	listCDNsProduct, listCDNsID, err := galaxyCommandArgument(inv.GalaxyListCDNs, "--galaxy-list-cdns")
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	installProduct, installBuild, err := galaxyCommandArgument(inv.GalaxyInstall, "--galaxy-install")
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}

	// --check-login-status answers before any login is attempted
	// (main.cpp:686-698).
	if inv.CheckLoginStatus {
		d, err := core.Open(ctx, inv.Config, ui, false)
		if err != nil {
			fmt.Fprintf(stderr, "Error: %v\n", err)
			return 1
		}
		if d.LoggedIn() {
			fmt.Fprintln(stdout, "Login status: Logged in")
			return 0
		}
		fmt.Fprintln(stdout, "Login status: Not logged in")
		return 1
	}

	// --logout clears the local login state and stops. It sits after the check
	// above — a query outranks a mutation, so both flags together answer the
	// query and remove nothing — and before the session below, because it must
	// never open one: a Downloader flushes its cookie jar on Close, which would
	// write the removed cookie file straight back.
	if inv.Logout {
		if err := logout(inv.Config, stdout); err != nil {
			fmt.Fprintf(stderr, "Error: %v\n", err)
			return 1
		}
		return 0
	}

	d, err := core.Open(ctx, inv.Config, ui, true)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	defer func() { _ = d.Close() }()

	// Downloader::init (main.cpp:802-806): a usable access token is checked
	// before any command runs, and a failure stops the run.
	if err := d.Init(ctx); err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}

	// The command chain, in the C++ order (main.cpp:810-931). Options this
	// build does not implement never reach here: they are recognised by the
	// parser and answered above.
	if inv.List {
		if err := renderList(ctx, d, inv, stdout); err != nil {
			fmt.Fprintf(stderr, "Error: %v\n", err)
			return 1
		}
		return 0
	}

	if showBuildsProduct != "" {
		res, err := d.ShowBuilds(ctx, showBuildsProduct, showBuildsID)
		// The Linux support messages are rendered even when the run then fails:
		// the C++ source prints them to stdout and this port reports the missing
		// fallback on stderr afterwards (downloader.cpp:4858-4863).
		renderNotice(stdout, stderr, res.Notice)
		if err != nil {
			fmt.Fprintf(stderr, "Error: %v\n", err)
			return 1
		}
		if res.Manifest != nil {
			if err := renderManifest(stdout, res.Manifest); err != nil {
				fmt.Fprintf(stderr, "Error: %v\n", err)
				return 1
			}
			return 0
		}
		if err := renderBuilds(stdout, res.Builds); err != nil {
			fmt.Fprintf(stderr, "Error: %v\n", err)
			return 1
		}
		return 0
	}

	if listCDNsProduct != "" {
		res, err := d.ListCDNs(ctx, listCDNsProduct, listCDNsID)
		renderNotice(stdout, stderr, res.Notice)
		if err != nil {
			fmt.Fprintf(stderr, "Error: %v\n", err)
			return 1
		}
		if err := renderCDNNames(stdout, res.Names); err != nil {
			fmt.Fprintf(stderr, "Error: %v\n", err)
			return 1
		}
		return 0
	}

	// --galaxy-install (main.cpp:886). The renderer paints the progress the
	// way printProgress does; failures take the same error path as every other
	// command rather than growing an exit code of their own (review D65a).
	if inv.GalaxyInstall != "" {
		req := core.NewInstallRequest(inv.Config, installProduct, installBuild)
		ui.attachRenderer(inv.Config)
		ui.renderer.Start()
		err := d.Install(ctx, req)
		ui.renderer.Stop()
		if err != nil {
			fmt.Fprintf(stderr, "Error: %v\n", err)
			return 1
		}
		return 0
	}

	return 0
}

// galaxyCommandArgument splits the "<product id or gamename>[/<build id or
// index>]" argument of the two Galaxy commands (main.cpp:840-845).
//
// An empty value means the command was not requested and is not an error.
// Anything beyond the second token is ignored, as it is upstream; an argument
// that produces no token at all is an error here, because the C++ source would
// read past the end of an empty vector.
func galaxyCommandArgument(value, option string) (productID, buildID string, err error) {
	if value == "" {
		return "", "", nil
	}
	tokens := util.Split(value, "/")
	if len(tokens) == 0 {
		return "", "", fmt.Errorf("%s: no product id in %q", option, value)
	}
	if len(tokens) == 2 {
		buildID = tokens[1]
	}
	return tokens[0], buildID, nil
}
