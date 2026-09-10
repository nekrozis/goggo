package config

// Ported from include/globalconstants.h:15-31 (namespace GlobalConstants).

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

// Unit formats accepted by the --unit-format option.
const (
	UnitFormatIEC uint32 = 1
	UnitFormatSI  uint32 = 2
)

// Unit divisors for IEC (binary) and SI (decimal) formatting.
const (
	UnitDivisorKIEC = 1024
	UnitDivisorMIEC = 1048576
	UnitDivisorKSI  = 1000
	UnitDivisorMSI  = 1000000
)

// Unit suffixes matching the divisor groups above.
const (
	UnitStringKIEC = "KiB"
	UnitStringMIEC = "MiB"
	UnitStringKSI  = "kB"
	UnitStringMSI  = "MB"
)

// Identity and version. This port keeps three concepts apart:
//
//   - ProgramName is the identity of this implementation (binary, CLI name and
//     the product token of the User-Agent).
//   - Version is this implementation's own version; it advances independently
//     of the upstream release the port follows.
//   - UpstreamCompatibilityVersion is the LGOGDownloader release whose
//     behaviour is being ported (CMakeLists.txt:74 PROJECT_VERSION). It is a
//     compatibility baseline, not this program's identity, so the CLI never
//     presents it as our version.
const (
	ProgramName                  = "goggo"
	Version                      = "0.1.0"
	UpstreamName                 = "LGOGDownloader"
	UpstreamCompatibilityVersion = "3.18"

	// VersionString is what the CLI prints as this program's own version.
	VersionString = ProgramName + " " + Version
)
