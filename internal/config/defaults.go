package config

import "runtime"

// NewConfig returns the configuration defaults applied before any command-line
// flag is parsed, plus the option defaults that belong to the config domain
// rather than the CLI.
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
	cfg.VersionNumber = Version
	cfg.Curl.UserAgent = DefaultUserAgent()

	// Directories. Paths are concatenated with "/", so a root that is set but
	// empty yields a root-relative path.
	cfg.CacheDirectory = cacheHome + "/" + ProgramName
	cfg.XMLDirectory = cfg.CacheDirectory + "/xml"
	cfg.ConfigDirectory = configHome + "/" + ProgramName
	cfg.Curl.CookiePath = cfg.ConfigDirectory + "/cookies.txt"
	cfg.ConfigFilePath = cfg.ConfigDirectory + "/config.cfg"
	cfg.BlacklistFilePath = cfg.ConfigDirectory + "/blacklist.txt"
	cfg.IgnorelistFilePath = cfg.ConfigDirectory + "/ignorelist.txt"
	cfg.TransformConfigFilePath = cfg.ConfigDirectory + "/transformations.json"

	// Option defaults. The CLI option table supplies the fields not set here.
	cfg.Directories.Directory = "."
	cfg.PlatformPriority = DefaultPlatformPriority
	cfg.LanguagePriority = DefaultLanguagePriority
	cfg.DownloadConfig.Include = IncludeAllMask()
	cfg.UnitFormat = UnitFormatIEC
	cfg.Retries = 3
	cfg.Wait = 0
	// Transfer guard: abort a transfer that stays below 200 B/s for 30 s. The
	// names cross over — LowSpeedTimeout is the duration in seconds,
	// LowSpeedTimeoutRate the rate in bytes per second.
	cfg.Curl.LowSpeedTimeout = 30
	cfg.Curl.LowSpeedTimeoutRate = 200
	cfg.Color = true   // --no-color clears it
	cfg.Unicode = true // --no-unicode clears it
	return cfg
}

// Option defaults shared with the CLI option table.
const (
	// DefaultPlatformPriority is the --platform default install priority.
	DefaultPlatformPriority = "w+l"

	// DefaultLanguagePriority is the --language default install priority.
	DefaultLanguagePriority = "en"
)

// IncludeAllMask is the mask `--include all` resolves to: the OR of every
// IncludeOptions entry. The custom-file bits are not offered by --include, so
// they are absent here even though the GFBase/GFDLC composites carry them.
func IncludeAllMask() uint32 {
	var mask uint32
	for _, o := range IncludeOptions {
		mask |= o.ID
	}
	return mask
}

// DefaultUserAgent builds the User-Agent string from this program's own
// identity and version, using Go's GOOS/GOARCH spellings. The User-Agent plays
// no part in authentication.
func DefaultUserAgent() string {
	return ProgramName + "/" + Version + " (" + runtime.GOOS + " " + runtime.GOARCH + ")"
}
