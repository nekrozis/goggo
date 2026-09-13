package cli

import (
	"strings"

	"github.com/nekrozis/goggo/internal/config"
	"github.com/nekrozis/goggo/internal/util"
)

// This file keeps the option vocabulary the parser and the command tree share:
// the values the parser declares as its own defaults, and the two syntax
// helpers that turn an option value into what the domain consumes.
//
// The parser itself lives in parse.go and the command tree in command.go; the
// previous front end (a boost-style option walk over one long name switch) was
// removed with CLI1 S2.

// Galaxy option defaults (main.cpp:329,340-345). They live here rather than in
// internal/config because they are boost default_value values, which the C++
// front end declares next to the options themselves; config.NewConfig gains the
// remaining option defaults with the full option table (S24).
//
// The download worker count is deliberately absent: its default is a product
// decision settled by the threads benchmark (review D9), not a value to inherit.
const (
	defaultGalaxyBuildSort     = "score"
	defaultGalaxyPlatform      = "w"
	defaultGalaxyLanguage      = "en"
	defaultGalaxyArch          = "x64"
	defaultGalaxyCDNPriority   = "edgecast,akamai_edgecast_proxy,fastly"
	defaultGalaxyInstallSubdir = "%install_dir%"
	defaultDirectory           = "./"

	// The progress interval stays within 1..10000 ms; an out-of-range value is
	// clamped to the nearest bound (main.cpp:519-523).
	progressIntervalMin = 1
	progressIntervalMax = 10000
)

// ensureTrailingSlash mirrors ensure_trailing_slash (main.cpp:28-40): an empty
// path becomes the fallback, and any other path gains a separator unless it
// already ends in one.
//
// Only a forward slash is tested, so a Windows path written with backslashes
// gains one — the same result the C++ source produces.
func ensureTrailingSlash(path, fallback string) string {
	if path == "" {
		return fallback
	}
	if !strings.HasSuffix(path, "/") {
		return path + "/"
	}
	return path
}

// optionMask resolves a comma-separated option list to its bit mask, mirroring
// the loop over `Util::tokenize(sIncludeOptions, ",")` in main.cpp:586-595.
func optionMask(list string, options []config.Option) uint32 {
	var mask uint32
	for _, item := range util.Split(list, ",") {
		mask |= util.OptionValue(item, options, false)
	}
	return mask
}
