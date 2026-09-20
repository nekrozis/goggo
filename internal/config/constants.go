package config

const (
	// GameDetailsCacheVersion is bumped whenever the cached game-details XML
	// format changes, invalidating previously cached files.
	GameDetailsCacheVersion = 7

	// ZlibWindowSize is the zlib window size used when decompressing Galaxy
	// depots.
	ZlibWindowSize = 15
)

// ProtocolPrefix is the URI scheme used for gogdownloader:// deep links.
const ProtocolPrefix = "gogdownloader://"

// UnitFormatIEC and UnitFormatSI are the unit formats accepted by the
// --unit-format option.
const (
	UnitFormatIEC uint32 = 1
	UnitFormatSI  uint32 = 2
)

// UnitDivisorKIEC and the following constants are the unit divisors for IEC
// (binary) and SI (decimal) formatting.
const (
	UnitDivisorKIEC = 1024
	UnitDivisorMIEC = 1048576
	UnitDivisorKSI  = 1000
	UnitDivisorMSI  = 1000000
)

// UnitStringKIEC and the following constants are the unit suffixes matching the
// divisor groups above.
const (
	UnitStringKIEC = "KiB"
	UnitStringMIEC = "MiB"
	UnitStringKSI  = "kB"
	UnitStringMSI  = "MB"
)

// ProgramName is this implementation's identity: the binary name, the CLI name
// and the product token of the User-Agent.
//
// Version is this implementation's own version; it advances independently of the
// compatibility baseline below. UpstreamCompatibilityVersion is the LGOGDownloader
// release whose behaviour this program follows — a compatibility baseline, not
// this program's identity, so the CLI never presents it as our version.
const (
	ProgramName                  = "goggo"
	Version                      = "0.1.0"
	UpstreamName                 = "LGOGDownloader"
	UpstreamCompatibilityVersion = "3.18"

	// VersionString is what the CLI prints as this program's own version.
	VersionString = ProgramName + " " + Version
)
