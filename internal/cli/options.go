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
// The parser itself lives in parse.go and the command tree in command.go.

// Galaxy option defaults. They live here rather than in internal/config because
// the parser declares them next to the options themselves.
//
// The download worker count is a front-end default too, but its value is a
// product decision settled by measurement; see defaultThreads.
const (
	defaultGalaxyBuildSort     = "score"
	defaultGalaxyPlatform      = "w"
	defaultGalaxyLanguage      = "en"
	defaultGalaxyArch          = "x64"
	defaultGalaxyCDNPriority   = "edgecast,akamai_edgecast_proxy,fastly"
	defaultGalaxyInstallSubdir = "%install_dir%"
	defaultDirectory           = "./"

	// defaultThreads is how many workers an install uses when --threads is
	// absent; measurement put the knee of the rate curve at 8.
	defaultThreads = 8

	// The progress interval stays within 1..10000 ms; an out-of-range value is
	// clamped to the nearest bound.
	progressIntervalMin = 1
	progressIntervalMax = 10000
)

// ensureTrailingSlash normalises a directory path: an empty path becomes the
// fallback, and any other path gains a separator unless it already ends in one.
//
// Only a forward slash is tested, so a Windows path written with backslashes
// gains one.
func ensureTrailingSlash(path, fallback string) string {
	if path == "" {
		return fallback
	}
	if !strings.HasSuffix(path, "/") {
		return path + "/"
	}
	return path
}

// optionMask resolves a comma-separated option list to its bit mask.
func optionMask(list string, options []config.Option) uint32 {
	var mask uint32
	for _, item := range util.Split(list, ",") {
		mask |= util.OptionValue(item, options, false)
	}
	return mask
}
