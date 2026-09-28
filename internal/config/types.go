package config

// DirectoryConfig holds the configured directories and the per-file-class
// subdirectory name templates.
type DirectoryConfig struct {
	Directory           string
	GameSubdir          string
	InstallersSubdir    string
	ExtrasSubdir        string
	PatchesSubdir       string
	LanguagePackSubdir  string
	DLCSubdir           string
	GalaxyInstallSubdir string
	SubDirectories      bool
}

// DLCSelector is one --dlc or --exclude-dlc value, kept in the order it was
// typed so a report can name the problems in that order.
//
// Value is either a DLC product id or a full DLC title; which one it is can only
// be decided against a selected build, so the parser stores it unresolved.
type DLCSelector struct {
	Value   string
	Exclude bool
}

// DownloadConfig holds the download-selection options. The installer
// platform/language fields and the priority lists use the bit flags and
// Option tables from options.go.
type DownloadConfig struct {
	// GalaxyLanguageRaw is the --language value as typed, "" when the flag was
	// absent. It stays a string: whether the selected build offers that
	// language is only known once the build manifest is fetched, so the
	// resolution belongs to the plan, not to the parser.
	GalaxyLanguageRaw   string
	PlatformPriority    []uint32
	LanguagePriority    []uint32
	GalaxyCDNPriority   []string
	Tags                []string
	DLCSelectors        []DLCSelector
	ChunkSize           int64 // in bytes
	InstallerPlatform   uint32
	InstallerLanguage   uint32
	Include             uint32
	GalaxyPlatform      uint32
	GalaxyArch          uint32
	SaveChangelogs      bool
	IgnoreDLCCount      bool
	SaveGameDetailsJSON bool
	SaveProductJSON     bool
	SaveLogo            bool
	SaveIcon            bool
	FreeSpaceCheck      bool
	SaveSerials         bool
	DuplicateHandler    bool
	GalaxyDependencies  bool
	DeleteOrphans       bool
	GalaxyLowercasePath bool
	CreateXML           bool
	RemoteXML           bool
}

// CurlConfig holds the transport options. Timeouts are in seconds.
type CurlConfig struct {
	CACertPath          string
	CookiePath          string
	UserAgent           string
	Timeout             int64
	LowSpeedTimeout     int64
	LowSpeedTimeoutRate int64
	VerifyPeer          bool
}

// Config is the complete configuration value passed down through the
// application layers.
type Config struct {
	VersionString           string
	IgnorelistFilePath      string
	GalaxyBuildSortingOrder string
	CacheDirectory          string
	XMLDirectory            string
	ConfigDirectory         string
	GameRegex               string
	Email                   string
	GameListFilePath        string
	BlacklistFilePath       string
	IgnoreDLCCountRegex     string
	PlatformPriority        string
	LanguagePriority        string
	Directories             DirectoryConfig
	Curl                    CurlConfig
	DownloadConfig          DownloadConfig
	Retries                 int
	Wait                    int
	ProgressInterval        int
	MsgLevel                int
	Threads                 uint32
	InfoThreads             uint32
	UnitFormat              uint32
	Login                   bool
	ForceBrowserLogin       bool
	Updated                 bool
	New                     bool
	IncludeHiddenProducts   bool
	SizeOnly                bool
	Unicode                 bool
	Color                   bool
	PlatformDetection       bool
	TrustAPIForExtras       bool
	UpdateCache             bool
}
