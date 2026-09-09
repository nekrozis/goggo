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
