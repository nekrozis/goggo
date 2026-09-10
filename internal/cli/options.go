package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/nekrozis/goggo/internal/config"
	"github.com/nekrozis/goggo/internal/util"
)

// Invocation is one parsed command line: the effective configuration plus the
// action the front end should take.
//
// Fields are ordered to minimise padding: the config and the strings first,
// then the uint32s, then the bools.
type Invocation struct {
	// Config is the effective configuration (defaults with the flags applied).
	Config config.Config

	// Unsupported names a requested feature this build does not implement. It
	// is reported as an error rather than silently succeeding.
	Unsupported string

	// GalaxyShowBuilds and GalaxyListCDNs carry the --galaxy-show-builds and
	// --galaxy-list-cdns arguments. Both are "<product id or gamename>[/<build
	// id or index>]"; an empty value means the command was not requested, which
	// is also what an explicitly empty argument means (main.cpp:839,888 test
	// the value for emptiness).
	GalaxyShowBuilds string
	GalaxyListCDNs   string

	// ListFormat is the --list format mask; 0 means --list was not given.
	ListFormat uint32

	Help             bool
	Version          bool
	CheckLoginStatus bool
	Logout           bool
	List             bool
}

// Galaxy option defaults (main.cpp:329,340). They live here rather than in
// internal/config because they are boost default_value values, which the C++
// front end declares next to the options themselves; config.NewConfig gains the
// remaining option defaults with the full table (S24).
const (
	defaultGalaxyBuildSort = "score"
	defaultGalaxyPlatform  = "w"
)

// Parse applies the command-line flags on top of cfg (the defaults), returning
// the resulting invocation.
//
// Precedence is deliberately two-layered — defaults, then flags — so the
// config file can later be inserted between them (S24) without reshaping the
// callers: it only has to change the cfg handed in here.
//
// Options the C++ front end has but this build does not implement are reported
// through Invocation.Unsupported when they are requested; unknown options are
// an error.
func Parse(args []string, cfg config.Config) (Invocation, error) {
	inv := Invocation{Config: cfg}

	// Galaxy option defaults, applied before any flag is read the way boost's
	// default_value does (main.cpp:329,340): the build sorting order and the
	// Galaxy platform.
	inv.Config.GalaxyBuildSortingOrder = defaultGalaxyBuildSort
	inv.Config.DownloadConfig.GalaxyPlatform = util.OptionValue(defaultGalaxyPlatform, config.Platforms, true)

	var (
		includeSeen string
		excludeSeen string
		includeSet  bool
		excludeSet  bool
	)

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") {
			return inv, fmt.Errorf("unexpected argument %q", arg)
		}
		name := strings.TrimLeft(arg, "-")
		value := ""
		hasValue := false
		if eq := strings.IndexByte(name, '='); eq >= 0 {
			value, hasValue = name[eq+1:], true
			name = name[:eq]
		}
		// takeValue consumes the next argument when no =value was supplied.
		takeValue := func() (string, error) {
			if hasValue {
				return value, nil
			}
			if i+1 >= len(args) {
				return "", fmt.Errorf("option --%s requires a value", name)
			}
			i++
			return args[i], nil
		}

		switch name {
		case "help":
			inv.Help = true
		case "version":
			inv.Version = true
		case "check-login-status":
			inv.CheckLoginStatus = true
		case "logout":
			// A goggo extension: upstream has no logout option and no remote
			// logout API (see dev/audit/S12.2-R5.md). It clears local state
			// only. The conflicts it refuses are checked after the loop.
			inv.Logout = true
		case "login":
			inv.Config.Login = true
		case "browser-login":
			// --browser-login selects the login path itself (main.cpp:472-475):
			// it must force a fresh login even when a session is already
			// stored, and Open's trigger (cfg.Login || !LoggedIn) does that
			// only when Login is set.
			inv.Config.ForceBrowserLogin = true
			inv.Config.Login = true
		case "login-email":
			v, err := takeValue()
			if err != nil {
				return inv, err
			}
			inv.Config.Email = v
		case "login-password":
			v, err := takeValue()
			if err != nil {
				return inv, err
			}
			inv.Config.Password = v
		case "list":
			// --list takes an optional format, defaulting to "games"
			// (implicit_value in the original).
			format := "games"
			if hasValue {
				format = value
			} else if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				format = args[i]
			}
			inv.List = true
			inv.ListFormat = util.OptionValue(format, config.ListFormats, false)
			if inv.ListFormat == 0 {
				inv.Unsupported = "--list " + format
			} else if inv.ListFormat != config.ListFormatGames &&
				inv.ListFormat != config.ListFormatTags &&
				inv.ListFormat != config.ListFormatWishlist {
				inv.Unsupported = "--list " + format
			}
		case "game":
			v, err := takeValue()
			if err != nil {
				return inv, err
			}
			inv.Config.GameRegex = v
		case "game-list":
			v, err := takeValue()
			if err != nil {
				return inv, err
			}
			inv.Config.GameListFilePath = v
		case "directory":
			v, err := takeValue()
			if err != nil {
				return inv, err
			}
			inv.Config.Directories.Directory = v
		case "platform":
			v, err := takeValue()
			if err != nil {
				return inv, err
			}
			priority, mask := util.ParseOptionString(v, config.Platforms)
			if mask == 0 {
				return inv, fmt.Errorf("invalid value for --platform: %q", v)
			}
			inv.Config.PlatformPriority = v
			inv.Config.DownloadConfig.PlatformPriority = priority
			inv.Config.DownloadConfig.InstallerPlatform = mask
		case "language":
			v, err := takeValue()
			if err != nil {
				return inv, err
			}
			priority, mask := util.ParseOptionString(v, config.Languages)
			if mask == 0 {
				return inv, fmt.Errorf("invalid value for --language: %q", v)
			}
			inv.Config.LanguagePriority = v
			inv.Config.DownloadConfig.LanguagePriority = priority
			inv.Config.DownloadConfig.InstallerLanguage = mask
		case "tags":
			v, err := takeValue()
			if err != nil {
				return inv, err
			}
			inv.Config.DownloadConfig.Tags = util.Split(v, ",")
		case "include":
			v, err := takeValue()
			if err != nil {
				return inv, err
			}
			includeSeen, includeSet = v, true
		case "exclude":
			v, err := takeValue()
			if err != nil {
				return inv, err
			}
			excludeSeen, excludeSet = v, true
		case "updated":
			inv.Config.Updated = true
		case "new":
			inv.Config.New = true
		case "no-platform-detection":
			inv.Config.PlatformDetection = false
		case "include-hidden-products":
			inv.Config.IncludeHiddenProducts = true
		case "no-color":
			inv.Config.Color = false
		case "respect-umask":
			inv.Config.RespectUmask = true
		case "cacert":
			v, err := takeValue()
			if err != nil {
				return inv, err
			}
			inv.Config.Curl.CACertPath = v
		case "retries":
			v, err := takeValue()
			if err != nil {
				return inv, err
			}
			n, err := strconv.Atoi(v)
			if err != nil {
				return inv, fmt.Errorf("invalid value for --retries: %q", v)
			}
			inv.Config.Retries = n
		case "wait":
			v, err := takeValue()
			if err != nil {
				return inv, err
			}
			n, err := strconv.Atoi(v)
			if err != nil {
				return inv, fmt.Errorf("invalid value for --wait: %q", v)
			}
			inv.Config.Wait = n
		case "unit-format":
			v, err := takeValue()
			if err != nil {
				return inv, err
			}
			switch strings.ToLower(v) {
			case "si":
				inv.Config.UnitFormat = config.UnitFormatSI
			case "iec":
				inv.Config.UnitFormat = config.UnitFormatIEC
			default:
				return inv, fmt.Errorf("invalid value for --unit-format: %q", v)
			}
		case "ignore-dlc-count":
			// implicit_value(".*") in the original.
			if hasValue {
				inv.Config.IgnoreDLCCountRegex = value
			} else {
				inv.Config.IgnoreDLCCountRegex = ".*"
			}
		case "galaxy-builds-sort":
			v, err := takeValue()
			if err != nil {
				return inv, err
			}
			// The C++ source stores the value without validating it; an
			// unknown order simply does not reorder anything (downloader
			// .cpp:6826-6830).
			inv.Config.GalaxyBuildSortingOrder = v
		case "galaxy-show-builds":
			v, err := takeValue()
			if err != nil {
				return inv, err
			}
			inv.GalaxyShowBuilds = v
		case "galaxy-list-cdns":
			v, err := takeValue()
			if err != nil {
				return inv, err
			}
			inv.GalaxyListCDNs = v
		case "galaxy-platform":
			v, err := takeValue()
			if err != nil {
				return inv, err
			}
			// Difference (recorded): Util::getOptionValue returns 0 for an
			// unmatched value and the C++ source then treats it as Windows; this
			// port rejects it, the way --platform already does.
			mask := util.OptionValue(v, config.Platforms, true)
			if mask == 0 {
				return inv, fmt.Errorf("invalid value for --galaxy-platform: %q", v)
			}
			inv.Config.DownloadConfig.GalaxyPlatform = mask
		// Recognised but not implemented in this build (review lock, W2/W3):
		// they fail loudly instead of pretending to work.
		case "save-config", "reset-config", "update-cache",
			"download", "repair", "status", "notifications", "create-xml",
			"check-orphans", "delete-orphans", "download-file", "output-file", "o",
			"clear-update-flags", "report":
			inv.Unsupported = "--" + name
		default:
			return inv, fmt.Errorf("unknown option --%s", name)
		}
	}

	if includeSet || excludeSet {
		var inc, exc uint32
		if includeSet {
			inc = optionMask(includeSeen, config.IncludeOptions)
		} else {
			inc = config.IncludeAllMask()
		}
		if excludeSet {
			exc = optionMask(excludeSeen, config.IncludeOptions)
		}
		inv.Config.DownloadConfig.Include = inc &^ exc
	}

	// --logout is a mutation that clears the whole local login state, so it is
	// refused alongside another action: quietly picking one of two contradictory
	// requests would be a surprise, and the named flag is what tells the user
	// which pair they actually typed (review ruling Q1/Q5). Only the first
	// conflict is reported.
	//
	// --check-login-status is deliberately NOT in this list: it is a query, and
	// the dispatcher answers it before any mutation happens (query > mutation),
	// so the pair is legal and removes nothing. --help/--version are likewise
	// left to the dispatcher, which already answers them first.
	if inv.Logout {
		switch {
		case inv.Config.ForceBrowserLogin:
			return inv, fmt.Errorf("--logout cannot be combined with --browser-login")
		case inv.Config.Login:
			return inv, fmt.Errorf("--logout cannot be combined with --login")
		case inv.List:
			return inv, fmt.Errorf("--logout cannot be combined with --list")
		}
	}
	return inv, nil
}

// optionMask resolves a comma-separated option list to its bit mask, mirroring
// the loop over `Util::tokenize(sIncludeOptions, ",")` in main.cpp:586-595.
func optionMask(list string, options []config.Option) uint32 {
	var mask uint32
	for _, item := range util.Split(list, ",") {
		mask |= util.OptionValue(item, options, false)
	}
	return mask
}

// usage writes the option help for the implemented subset.
func usage(w io.Writer) {
	fmt.Fprintf(w, "Usage: %s [options]\n", config.ProgramName)
	fmt.Fprint(w, `
Options:
  --login                     Login
  --browser-login             Login (force browser login)
  --check-login-status        Check login status (exit code 0 when logged in)
  --logout                    Log out (clear local login state)
  --login-email <email>       Login email
  --login-password <pass>     Login password
  --list [format]             List games/tags/wishlist (default: games)
  --game <regex>              Regular expression filter for list
  --game-list <file>          File with a list of regular expressions
  --platform <spec>           Installer platform priority (default: `+config.DefaultPlatformPriority+`)
  --language <spec>           Installer language priority (default: `+config.DefaultLanguagePriority+`)
  --directory <path>          Download directory (default: .)
  --tags <a,b>                Filter by account tags
  --updated                   Only games with the update flag set
  --new                       Only games with the new flag set
  --include <spec>            What to include (default: all)
  --exclude <spec>            What to exclude
  --no-platform-detection     Skip platform detection
  --include-hidden-products   Include hidden products
  --ignore-dlc-count [regex]  Ignore DLC count information (default: .*)
  --unit-format <IEC|SI>      Unit format (default: IEC)
  --retries <n>               Maximum number of retries (default: 3)
  --wait <microseconds>       Time to wait between requests
  --galaxy-builds-sort <s>    Sorting order for Galaxy builds (date|score|none, default: score)
  --galaxy-show-builds <id>   Show game builds (<product id or gamename>[/<build id or index>])
  --galaxy-list-cdns <id>     List available CDNs (<product id or gamename>[/<build id or index>])
  --galaxy-platform <spec>    Galaxy platform (default: w)
  --cacert <path>             CA certificate bundle in PEM format
  --no-color                  Don't use coloring in the status messages
  --respect-umask             Do not adjust permissions of sensitive files
  --help                      Show this help
  --version                   Show version
`)
}
