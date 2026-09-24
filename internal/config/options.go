package config

// Option is one selectable value of a command-line option group (languages,
// platforms, game-file types,...). ID is a single-bit (or composite) flag
// mask, Code is the canonical short name used on the command line, Name is a
// human-readable string and Regexp matches user-supplied aliases.
type Option struct {
	Code   string
	Name   string
	Regexp string
	ID     uint32
}

// LangEN and the following constants are the language bit flags used by the
// Languages table.
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

// Languages is the full language table.
var Languages = []Option{
	{"en", "English", "en|eng|english|en[_-]US", LangEN},
	{"de", "German", "de|deu|ger|german|de[_-]DE", LangDE},
	{"fr", "French", "fr|fra|fre|french|fr[_-]FR", LangFR},
	{"pl", "Polish", "pl|pol|polish|pl[_-]PL", LangPL},
	{"ru", "Russian", "ru|rus|russian|ru[_-]RU", LangRU},
	{"cn", "Chinese", "cn|zh|zho|chi|chinese|zh[_-](CN|Hans)", LangCN},
	{"cz", "Czech", "cz|cs|ces|cze|czech|cs[_-]CZ", LangCZ},
	{"es", "Spanish", "es|spa|spanish|es[_-]ES", LangES},
	{"hu", "Hungarian", "hu|hun|hungarian|hu[_-]HU", LangHU},
	{"it", "Italian", "it|ita|italian|it[_-]IT", LangIT},
	{"jp", "Japanese", "jp|ja|jpn|japanese|ja[_-]JP", LangJP},
	{"tr", "Turkish", "tr|tur|turkish|tr[_-]TR", LangTR},
	{"pt", "Portuguese", "pt|por|portuguese|pt[_-]PT", LangPT},
	{"ko", "Korean", "ko|kor|korean|ko[_-]KR", LangKO},
	{"nl", "Dutch", "nl|nld|dut|dutch|nl[_-]NL", LangNL},
	{"sv", "Swedish", "sv|swe|swedish|sv[_-]SE", LangSV},
	{"no", "Norwegian", "no|nor|norwegian|nb[_-]no|nn[_-]NO", LangNO},
	{"da", "Danish", "da|dan|danish|da[_-]DK", LangDA},
	{"fi", "Finnish", "fi|fin|finnish|fi[_-]FI", LangFI},
	{"br", "Brazilian Portuguese", "br|pt_br|pt-br|ptbr|brazilian_portuguese", LangPTBR},
	{"sk", "Slovak", "sk|slk|slo|slovak|sk[_-]SK", LangSK},
	{"bl", "Bulgarian", "bl|bg|bul|bulgarian|bg[_-]BG", LangBL},
	{"uk", "Ukrainian", "uk|ukr|ukrainian|uk[_-]UA", LangUK},
	{"es_mx", "Spanish (Latin American)", "es_mx|es-mx|esmx|es-419|spanish_latin_american", LangES419},
	{"ar", "Arabic", "ar|ara|arabic|ar[_-][A-Z]{2}", LangAR},
	{"ro", "Romanian", "ro|ron|rum|romanian|ro[_-][RM]O", LangRO},
	{"he", "Hebrew", "he|heb|hebrew|he[_-]IL", LangHE},
	{"th", "Thai", "th|tha|thai|th[_-]TH", LangTH},
}

// PlatformWindows and the following constants are the installer platform bit
// flags.
const (
	PlatformWindows uint32 = 1 << iota
	PlatformMac
	PlatformLinux
)

// Platforms is the installer platform table.
var Platforms = []Option{
	{"win", "Windows", "w|win|windows", PlatformWindows},
	{"mac", "Mac", "m|mac|osx", PlatformMac},
	{"linux", "Linux", "l|lin|linux", PlatformLinux},
}

// ArchX86 and ArchX64 are the Galaxy depot architecture bit flags.
const (
	ArchX86 uint32 = 1 << iota
	ArchX64
)

// GalaxyArchs is the Galaxy depot architecture table.
var GalaxyArchs = []Option{
	{"32", "32-bit", "32|x86|32bit|32-bit", ArchX86},
	{"64", "64-bit", "64|x64|64bit|64-bit", ArchX64},
}

// ListFormatGames and the following constants are the --list format bit flags.
const (
	ListFormatGames uint32 = 1 << iota
	ListFormatDetailsText
	ListFormatDetailsJSON
	ListFormatTags
	ListFormatTransformations
	ListFormatUserdata
	ListFormatWishlist
)

// ListFormats is the --list format table.
var ListFormats = []Option{
	{"games", "Games", "g|games", ListFormatGames},
	{"details", "Details", "d|details", ListFormatDetailsText},
	{"json", "JSON", "j|json", ListFormatDetailsJSON},
	{"tags", "Tags", "t|tags", ListFormatTags},
	{"transform", "Transformations", "tr|transform|transformations", ListFormatTransformations},
	{"userdata", "User data", "ud|userdata", ListFormatUserdata},
	{"wishlist", "Wishlist", "w|wishlist", ListFormatWishlist},
}

// GFBaseInstaller and the following constants are the game-file type bit flags.
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

// GFBase and the following constants are composite masks combining the
// game-file type bits above.
const (
	GFBase      = GFBaseInstaller | GFBaseExtra | GFBasePatch | GFBaseLangPack | GFCustomBase
	GFDLC       = GFDLCInstaller | GFDLCExtra | GFDLCPatch | GFDLCLangPack | GFCustomDLC
	GFInstaller = GFBaseInstaller | GFDLCInstaller
	GFExtra     = GFBaseExtra | GFDLCExtra
	GFPatch     = GFBasePatch | GFDLCPatch
	GFLangPack  = GFBaseLangPack | GFDLCLangPack
	GFCustom    = GFCustomBase | GFCustomDLC
)

// IncludeOptions is the --include/--exclude option table.
var IncludeOptions = []Option{
	{"bi", "Base game installers", "bi|basegame_installers", GFBaseInstaller},
	{"be", "Base game extras", "be|basegame_extras", GFBaseExtra},
	{"bp", "Base game patches", "bp|basegame_patches", GFBasePatch},
	{"bl", "Base game language packs", "bl|basegame_languagepacks|basegame_langpacks", GFBaseLangPack},
	{"di", "DLC installers", "di|dlc_installers", GFDLCInstaller},
	{"de", "DLC extras", "de|dlc_extras", GFDLCExtra},
	{"dp", "DLC patches", "dp|dlc_patches", GFDLCPatch},
	{"dl", "DLC language packs", "dl|dlc_languagepacks|dlc_langpacks", GFDLCLangPack},
	{"d", "DLCs", "d|dlc|dlcs", GFDLC},
	{"b", "Basegame", "b|bg|basegame", GFBase},
	{"i", "All installers", "i|installers", GFInstaller},
	{"e", "All extras", "e|extras", GFExtra},
	{"p", "All patches", "p|patches", GFPatch},
	{"l", "All language packs", "l|languagepacks|langpacks", GFLangPack},
}
