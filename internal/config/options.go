package config

// Option is one selectable value of a command-line option group (languages,
// platforms, game-file types, ...). ID is a single-bit (or composite) flag
// mask, Code is the canonical short name used on the command line, Name is a
// human-readable string and Regexp matches user-supplied aliases.
type Option struct {
	ID     uint32
	Code   string
	Name   string
	Regexp string
}

// Game detail cache entry versions and format markers are defined in
// constants.go; this file ports the option tables from
// include/globalconstants.h:30-174.

// Language bit flags. Values match the original 1<<N assignment order.
const (
	LangEN uint32 = 1 << iota
	LangDE
	LangFR
	LangPL
	LangRU
	LangCN
	LangCZ
	LangES
	LangHU
	LangIT
	LangJP
	LangTR
	LangPT
	LangKO
	LangNL
	LangSV
	LangNO
	LangDA
	LangFI
	LangPTBR
	LangSK
	LangBL
	LangUK
	LangES419
	LangAR
	LangRO
	LangHE
	LangTH
)

// Languages is the full language table (globalconstants.h:63-93).
var Languages = []Option{
	{LangEN, "en", "English", "en|eng|english|en[_-]US"},
	{LangDE, "de", "German", "de|deu|ger|german|de[_-]DE"},
	{LangFR, "fr", "French", "fr|fra|fre|french|fr[_-]FR"},
	{LangPL, "pl", "Polish", "pl|pol|polish|pl[_-]PL"},
	{LangRU, "ru", "Russian", "ru|rus|russian|ru[_-]RU"},
	{LangCN, "cn", "Chinese", "cn|zh|zho|chi|chinese|zh[_-](CN|Hans)"},
	{LangCZ, "cz", "Czech", "cz|cs|ces|cze|czech|cs[_-]CZ"},
	{LangES, "es", "Spanish", "es|spa|spanish|es[_-]ES"},
	{LangHU, "hu", "Hungarian", "hu|hun|hungarian|hu[_-]HU"},
	{LangIT, "it", "Italian", "it|ita|italian|it[_-]IT"},
	{LangJP, "jp", "Japanese", "jp|ja|jpn|japanese|ja[_-]JP"},
	{LangTR, "tr", "Turkish", "tr|tur|turkish|tr[_-]TR"},
	{LangPT, "pt", "Portuguese", "pt|por|portuguese|pt[_-]PT"},
	{LangKO, "ko", "Korean", "ko|kor|korean|ko[_-]KR"},
	{LangNL, "nl", "Dutch", "nl|nld|dut|dutch|nl[_-]NL"},
	{LangSV, "sv", "Swedish", "sv|swe|swedish|sv[_-]SE"},
	{LangNO, "no", "Norwegian", "no|nor|norwegian|nb[_-]no|nn[_-]NO"},
	{LangDA, "da", "Danish", "da|dan|danish|da[_-]DK"},
	{LangFI, "fi", "Finnish", "fi|fin|finnish|fi[_-]FI"},
	{LangPTBR, "br", "Brazilian Portuguese", "br|pt_br|pt-br|ptbr|brazilian_portuguese"},
	{LangSK, "sk", "Slovak", "sk|slk|slo|slovak|sk[_-]SK"},
	{LangBL, "bl", "Bulgarian", "bl|bg|bul|bulgarian|bg[_-]BG"},
	{LangUK, "uk", "Ukrainian", "uk|ukr|ukrainian|uk[_-]UA"},
	{LangES419, "es_mx", "Spanish (Latin American)", "es_mx|es-mx|esmx|es-419|spanish_latin_american"},
	{LangAR, "ar", "Arabic", "ar|ara|arabic|ar[_-][A-Z]{2}"},
	{LangRO, "ro", "Romanian", "ro|ron|rum|romanian|ro[_-][RM]O"},
	{LangHE, "he", "Hebrew", "he|heb|hebrew|he[_-]IL"},
	{LangTH, "th", "Thai", "th|tha|thai|th[_-]TH"},
}

// Platform bit flags.
const (
	PlatformWindows uint32 = 1 << iota
	PlatformMac
	PlatformLinux
)

// Platforms is the installer platform table (globalconstants.h:100-105).
var Platforms = []Option{
	{PlatformWindows, "win", "Windows", "w|win|windows"},
	{PlatformMac, "mac", "Mac", "m|mac|osx"},
	{PlatformLinux, "linux", "Linux", "l|lin|linux"},
}

// Galaxy architecture bit flags.
const (
	ArchX86 uint32 = 1 << iota
	ArchX64
)

// GalaxyArchs is the Galaxy depot architecture table (globalconstants.h:111-115).
var GalaxyArchs = []Option{
	{ArchX86, "32", "32-bit", "32|x86|32bit|32-bit"},
	{ArchX64, "64", "64-bit", "64|x64|64bit|64-bit"},
}

// --list format bit flags.
const (
	ListFormatGames uint32 = 1 << iota
	ListFormatDetailsText
	ListFormatDetailsJSON
	ListFormatTags
	ListFormatTransformations
	ListFormatUserdata
	ListFormatWishlist
)

// ListFormats is the --list format table (globalconstants.h:125-134).
var ListFormats = []Option{
	{ListFormatGames, "games", "Games", "g|games"},
	{ListFormatDetailsText, "details", "Details", "d|details"},
	{ListFormatDetailsJSON, "json", "JSON", "j|json"},
	{ListFormatTags, "tags", "Tags", "t|tags"},
	{ListFormatTransformations, "transform", "Transformations", "tr|transform|transformations"},
	{ListFormatUserdata, "userdata", "User data", "ud|userdata"},
	{ListFormatWishlist, "wishlist", "Wishlist", "w|wishlist"},
}

// Game-file type bit flags (globalconstants.h:136-156).
const (
	GFBaseInstaller uint32 = 1 << iota
	GFBaseExtra
	GFBasePatch
	GFBaseLangPack
	GFDLCInstaller
	GFDLCExtra
	GFDLCPatch
	GFDLCLangPack
	GFCustomBase
	GFCustomDLC
)

// Composite game-file masks mirroring GFTYPE_* in the original header.
const (
	GFBase      = GFBaseInstaller | GFBaseExtra | GFBasePatch | GFBaseLangPack | GFCustomBase
	GFDLC       = GFDLCInstaller | GFDLCExtra | GFDLCPatch | GFDLCLangPack | GFCustomDLC
	GFInstaller = GFBaseInstaller | GFDLCInstaller
	GFExtra     = GFBaseExtra | GFDLCExtra
	GFPatch     = GFBasePatch | GFDLCPatch
	GFLangPack  = GFBaseLangPack | GFDLCLangPack
	GFCustom    = GFCustomBase | GFCustomDLC
)

// IncludeOptions is the --include/--exclude option table
// (globalconstants.h:158-174).
var IncludeOptions = []Option{
	{GFBaseInstaller, "bi", "Base game installers", "bi|basegame_installers"},
	{GFBaseExtra, "be", "Base game extras", "be|basegame_extras"},
	{GFBasePatch, "bp", "Base game patches", "bp|basegame_patches"},
	{GFBaseLangPack, "bl", "Base game language packs", "bl|basegame_languagepacks|basegame_langpacks"},
	{GFDLCInstaller, "di", "DLC installers", "di|dlc_installers"},
	{GFDLCExtra, "de", "DLC extras", "de|dlc_extras"},
	{GFDLCPatch, "dp", "DLC patches", "dp|dlc_patches"},
	{GFDLCLangPack, "dl", "DLC language packs", "dl|dlc_languagepacks|dlc_langpacks"},
	{GFDLC, "d", "DLCs", "d|dlc|dlcs"},
	{GFBase, "b", "Basegame", "b|bg|basegame"},
	{GFInstaller, "i", "All installers", "i|installers"},
	{GFExtra, "e", "All extras", "e|extras"},
	{GFPatch, "p", "All patches", "p|patches"},
	{GFLangPack, "l", "All language packs", "l|languagepacks|langpacks"},
}
