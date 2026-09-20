package config

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

// Galaxy OAuth client identity and redirect URI: protocol/client credentials —
// goggo's Galaxy client identity to the OAuth server; not a user authentication
// secret. They are the same for every user, they are not derived from anyone's
// account, and they are the fallback whenever the stored token JSON carries no
// client_id/client_secret override. The user authentication secrets — the access
// token, the refresh token, the cookies, the password — are a different class and
// are not part of this table.
const (
	DefaultClientID     = "46899977096215655"
	DefaultClientSecret = "9d85c43b1482497dbbce61f6e4aa173a433796eeae2ca8c5f6129f2dc4de46d9"
	DefaultRedirectURI  = "https://embed.gog.com/on_login_success?origin=client"
)

// ProgramName is this implementation's identity: the binary name, the CLI name and
// the User-Agent product token. Version is this implementation's own version, and
// UpstreamCompatibilityVersion is the LGOGDownloader release whose behaviour this
// program follows — a compatibility baseline, not this program's identity, so the
// CLI never presents it as our version.
const (
	ProgramName                  = "goggo"
	Version                      = "0.1.0"
	UpstreamName                 = "LGOGDownloader"
	UpstreamCompatibilityVersion = "3.18"

	// VersionString is what the CLI prints as this program's own version.
	VersionString = ProgramName + " " + Version
)
