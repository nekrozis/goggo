package config

// DirectoryConfig holds the configured directories and the per-file-class
// subdirectory name templates.
type DirectoryConfig struct {
	Directory           string
	WinePrefix          string
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
	GalaxyCDN         uint32
	Include           uint32
	GalaxyPlatform    uint32
	GalaxyLanguage    uint32
	GalaxyArch        uint32

	RemoteXML            bool
	SaveChangelogs       bool
	SaveSerials          bool
	SaveGameDetailsJSON  bool
	SaveProductJSON      bool
	SaveLogo             bool
	SaveIcon             bool
	AutomaticXMLCreation bool
	FreeSpaceCheck       bool
	IgnoreDLCCount       bool
	DuplicateHandler     bool
	GalaxyDependencies   bool
	DeleteOrphans        bool
	GalaxyLowercasePath  bool
}

// GameSpecificConfig groups the per-game directory and download settings.
type GameSpecificConfig struct {
	Directory DirectoryConfig
	Download  DownloadConfig
}

// CurlConfig holds the transport options. Timeouts are in seconds;
// DownloadRate is in bytes per second.
type CurlConfig struct {
	CACertPath          string
	CookiePath          string
	UserAgent           string
	Interface           string
	Timeout             int64
	DownloadRate        int64
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

	CloudWhiteList []string
	CloudBlackList []string

	FileID                  string
	OutputFilename          string
	CacheDirectory          string
	XMLDirectory            string
	ConfigDirectory         string
	ConfigFilePath          string
	BlacklistFilePath       string
	IgnorelistFilePath      string
	GameHasDLCListFilePath  string
	ReportFilePath          string
	TransformConfigFilePath string
	GameListFilePath        string
	XMLFile                 string
	GameRegex               string
	OrphanRegex             string
	IgnoreDLCCountRegex     string
	PlatformPriority        string
	LanguagePriority        string
	VersionString           string
	VersionNumber           string
	Email                   string
	Password                string
	GalaxyBuildSortingOrder string

	ChunkSize        uint64
	Retries          int
	CacheValid       int
	Wait             int
	ProgressInterval int
	MsgLevel         int

	Threads     uint32
	InfoThreads uint32
	ListFormat  uint32
	UnitFormat  uint32

	Login                 bool
	ForceBrowserLogin     bool
	SaveConfig            bool
	ResetConfig           bool
	Download              bool
	Repair                bool
	Updated               bool
	New                   bool
	CheckStatus           bool
	Notifications         bool
	IncludeHiddenProducts bool
	SizeOnly              bool
	Unicode               bool
	Color                 bool
	Report                bool
	RespectUmask          bool
	PlatformDetection     bool
	UseFastCheck          bool
	TrustAPIForExtras     bool
	GalaxyListCDNs        bool
	UseCache              bool
	UpdateCache           bool
	CloudForce            bool
}
