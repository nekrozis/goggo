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

// DownloadConfig holds the download-selection options. The installer
// platform/language fields and the priority lists use the bit flags and
// Option tables from options.go.
type DownloadConfig struct {
	PlatformPriority  []uint32
	LanguagePriority  []uint32
	GalaxyCDNPriority []string
	Tags              []string

	InstallerPlatform uint32
	InstallerLanguage uint32
	Include           uint32
	GalaxyPlatform    uint32
	GalaxyLanguage    uint32
	GalaxyArch        uint32

	RemoteXML           bool
	SaveChangelogs      bool
	SaveSerials         bool
	SaveGameDetailsJSON bool
	SaveProductJSON     bool
	SaveLogo            bool
	SaveIcon            bool
	FreeSpaceCheck      bool
	IgnoreDLCCount      bool
	DuplicateHandler    bool
	GalaxyDependencies  bool
	DeleteOrphans       bool
	GalaxyLowercasePath bool
}

// GameSpecificConfig groups the per-game directory and download settings.
type GameSpecificConfig struct {
	Directory DirectoryConfig
	Download  DownloadConfig
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
	Verbose             bool
}

// Config is the complete configuration value passed down through the
// application layers.
type Config struct {
	Directories    DirectoryConfig
	DownloadConfig DownloadConfig
	Curl           CurlConfig

	CacheDirectory          string
	XMLDirectory            string
	ConfigDirectory         string
	BlacklistFilePath       string
	IgnorelistFilePath      string
	GameListFilePath        string
	GameRegex               string
	IgnoreDLCCountRegex     string
	PlatformPriority        string
	LanguagePriority        string
	VersionString           string
	Email                   string
	Password                string
	GalaxyBuildSortingOrder string

	Retries          int
	Wait             int
	ProgressInterval int
	MsgLevel         int

	Threads     uint32
	InfoThreads uint32
	UnitFormat  uint32

	Login                 bool
	ForceBrowserLogin     bool
	Download              bool
	Updated               bool
	New                   bool
	IncludeHiddenProducts bool
	SizeOnly              bool
	Unicode               bool
	Color                 bool
	Report                bool
	PlatformDetection     bool
	TrustAPIForExtras     bool
	UpdateCache           bool
}
