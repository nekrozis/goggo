package config

// Configuration structs ported from include/config.h.

// DirectoryConfig mirrors struct DirectoryConfig (config.h:18-30).
type DirectoryConfig struct {
	SubDirectories      bool
	Directory           string
	WinePrefix          string
	GameSubdir          string
	InstallersSubdir    string
	ExtrasSubdir        string
	PatchesSubdir       string
	LanguagePackSubdir  string
	DLCSubdir           string
	GalaxyInstallSubdir string
}

// DownloadConfig mirrors struct DownloadConfig (config.h:32-61). Installer
// platform/language fields and the priority lists use the bit flags and
// Option tables from options.go.
type DownloadConfig struct {
	InstallerPlatform uint32
	InstallerLanguage uint32
	GalaxyCDN         uint32
	PlatformPriority  []uint32
	LanguagePriority  []uint32
	GalaxyCDNPriority []string
	Tags              []string
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

	IgnoreDLCCount      bool
	DuplicateHandler    bool
	GalaxyDependencies  bool
	DeleteOrphans       bool
	GalaxyLowercasePath bool
}

// GameSpecificConfig mirrors struct gameSpecificConfig (config.h:63-67).
type GameSpecificConfig struct {
	Download  DownloadConfig
	Directory DirectoryConfig
}

// CurlConfig mirrors struct CurlConfig (config.h:215-227). Timeouts are in
// seconds; DownloadRate is in bytes per second.
type CurlConfig struct {
	VerifyPeer          bool
	Verbose             bool
	CACertPath          string
	CookiePath          string
	UserAgent           string
	Timeout             int64
	DownloadRate        int64
	LowSpeedTimeout     int64
	LowSpeedTimeoutRate int64
	Interface           string
}

// Config mirrors struct Config (config.h:229-328).
//
// Deferred by design (see doc.go):
//   - blacklist/ignorelist (config.h:308-309): Blacklist semantics live in
//     include/blacklist.h and are ported with the filter work.
//   - transformationsJSON (config.h:327): parsing lands with the
//     transformations feature.
type Config struct {
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

	UseCache    bool
	UpdateCache bool
	CacheValid  int

	FileID         string
	OutputFilename string

	Curl CurlConfig

	DownloadConfig DownloadConfig

	Directories DirectoryConfig

	CacheDirectory  string
	XMLDirectory    string
	ConfigDirectory string

	ConfigFilePath          string
	BlacklistFilePath       string
	IgnorelistFilePath      string
	GameHasDLCListFilePath  string
	ReportFilePath          string
	TransformConfigFilePath string
	GameListFilePath        string

	XMLFile string

	GameRegex           string
	OrphanRegex         string
	IgnoreDLCCountRegex string

	PlatformPriority string
	LanguagePriority string

	VersionString           string
	VersionNumber           string
	Email                   string
	Password                string
	GalaxyBuildSortingOrder string

	CloudWhiteList []string
	CloudBlackList []string
	CloudForce     bool

	Retries          int
	Threads          uint32
	InfoThreads      uint32
	Wait             int
	ChunkSize        uint64
	ProgressInterval int
	MsgLevel         int
	ListFormat       uint32
	UnitFormat       uint32
}
