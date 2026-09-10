package config

import "runtime"

// NewConfig returns the configuration defaults main.cpp establishes before any
// command-line flag is applied (main.cpp:67-84), plus the option defaults that
// belong to the config domain rather than the CLI.
//
// It is a pure constructor: it opens no files, creates no directories and reads
// no environment. The two XDG roots are passed in rather than looked up here,
// both to keep this function free of side effects and to avoid a config -> util
// dependency: util already depends on config for the option tables, so
// importing it here would close an import cycle. Callers resolve the roots
// through util.ConfigHome/util.CacheHome, which remain the single entry point
// for path resolution.
func NewConfig(configHome, cacheHome string) Config {
	var cfg Config

	cfg.VersionString = VersionString
	cfg.VersionNumber = VersionNumber
	cfg.Curl.UserAgent = DefaultUserAgent()

	// Directories (main.cpp:71-84). The paths follow the C++ concatenation
	// exactly, so an XDG variable that is set but empty yields the same
	// root-relative path as the original.
	cfg.CacheDirectory = cacheHome + "/lgogdownloader"
	cfg.XMLDirectory = cfg.CacheDirectory + "/xml"
	cfg.ConfigDirectory = configHome + "/lgogdownloader"
	cfg.Curl.CookiePath = cfg.ConfigDirectory + "/cookies.txt"
	cfg.ConfigFilePath = cfg.ConfigDirectory + "/config.cfg"
	cfg.BlacklistFilePath = cfg.ConfigDirectory + "/blacklist.txt"
	cfg.IgnorelistFilePath = cfg.ConfigDirectory + "/ignorelist.txt"
	cfg.TransformConfigFilePath = cfg.ConfigDirectory + "/transformations.json"

	// Option defaults (main.cpp:276-323). Only the subset the S12 CLI
	// registers is set here; the remaining defaults are added with the full
	// option table in S24.
	cfg.Directories.Directory = "."
	cfg.PlatformPriority = DefaultPlatformPriority
	cfg.LanguagePriority = DefaultLanguagePriority
	cfg.DownloadConfig.Include = IncludeAllMask()
	cfg.UnitFormat = UnitFormatIEC
	cfg.Retries = 3
	cfg.Wait = 0
	cfg.Color = true // --no-color clears it
	return cfg
}

// Option defaults shared with the CLI option table (main.cpp:280-281,307-308).
const (
	// DefaultPlatformPriority is the --platform default install priority.
	DefaultPlatformPriority = "w+l"

	// DefaultLanguagePriority is the --language default install priority.
	DefaultLanguagePriority = "en"
)

// IncludeAllMask is the mask `--include all` resolves to: the OR of every
// IncludeOptions entry, exactly like Util::getOptionValue("all", ...). The
// custom-file bits are not offered by --include, so they are absent here even
// though the GFBase/GFDLC composites carry them.
func IncludeAllMask() uint32 {
	var mask uint32
	for _, o := range IncludeOptions {
		mask |= o.ID
	}
	return mask
}

// DefaultUserAgent builds the User-Agent string. The C++ value is composed at
// build time from CMAKE_SYSTEM_NAME/CMAKE_SYSTEM_PROCESSOR (CMakeLists.txt:76);
// the Go port uses the runtime equivalents, which is an intentional
// difference recorded in the audit.
func DefaultUserAgent() string {
	return "LGOGDownloader/" + VersionNumber + " (" + runtime.GOOS + " " + runtime.GOARCH + ")"
}
