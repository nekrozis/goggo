package cli

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/nekrozis/goggo/internal/config"
	"github.com/nekrozis/goggo/internal/util"
)

// msgLevelVerbose is the level --verbose selects. It mirrors MSGLEVEL_VERBOSE
// (message.h:19) the way core does; keeping the two in step is the price of
// core owning its own copy of the upstream enum.
const msgLevelVerbose = 1

// optionID identifies one option. The ids exist so the command tree can state
// what it accepts as data (command.go) instead of a chain of name comparisons.
type optionID uint8

const (
	optHelp optionID = iota
	optVersion

	// Shared options: shared semantics, not unconditional acceptance (D15).
	optVerbose
	optNoColor
	optNoUnicode
	optUnitFormat
	optRetries
	optWait
	optTimeout

	// The installation-locating group (see installTargetOptions).
	optDirectory
	optInstallDir
	optNoSubdirectories
	optPlatform
	optLanguage
	optArch

	// install only.
	optThreads
	optProgressInterval
	optCDNPriority
	optNoDependencies
	optCheckFreeSpace

	// Listing filters.
	optTag
	optGame
	optGameList
	optUpdated
	optNew
	optIncludeHidden
	optIgnoreDLCCount
	optInstallerPlatform
	optInstallerLanguage
	optInclude
	optExclude
	optBlacklist
	optIgnorelist

	// show builds.
	optSort

	// Destructive confirmation (orphans remove only, D16).
	optYes

	// auth login.
	optBrowser
	optEmail
)

// sharedOptions are accepted by every command that renders output or talks to
// the network, plus the two meta shortcuts. They are shared *semantics*; a
// command still has to be one that can meaningfully use them, and the parser
// checks the node's own list on top of this one.
var sharedOptions = optionSet{
	optHelp,
	optVersion,
	optVerbose,
	optNoColor,
	optNoUnicode,
	optUnitFormat,
	optRetries,
	optWait,
	optTimeout,
}

// valueMode says how an option takes its value.
type valueMode uint8

const (
	valueNone     valueMode = iota // a switch
	valueRequired                  // --opt value and --opt=value both work
	valueOptional                  // --opt, or --opt value when one follows
)

// optionSpec is one option: its names, its value shape, its help text and the
// single place its value becomes part of the invocation. The parser is generic;
// everything option-specific lives here, which is what keeps the parse loop
// from growing a name-by-name chain.
type optionSpec struct {
	id      optionID
	long    string
	aliases []string
	value   valueMode
	hidden  bool
	arg     string
	summary string
	parse   func(inv *invocation, value string) error
}

// optionTable is the CLI's complete option vocabulary (CLI1 v8 §5).
var optionTable = []optionSpec{
	{id: optHelp, long: "help", aliases: []string{"h"}, summary: "Show help"},
	{id: optVersion, long: "version", summary: "Show version"},
	{
		id: optVerbose, long: "verbose", aliases: []string{"v"},
		summary: "Verbose output (per-file records, skipped files)",
		parse:   func(inv *invocation, _ string) error { inv.cfg.MsgLevel = msgLevelVerbose; return nil },
	},
	{
		id: optNoColor, long: "no-color",
		summary: "Do not use coloring in status messages",
		parse:   func(inv *invocation, _ string) error { inv.cfg.Color = false; return nil },
	},
	{
		id: optNoUnicode, long: "no-unicode",
		summary: "Do not use Unicode in the progress bar",
		parse:   func(inv *invocation, _ string) error { inv.cfg.Unicode = false; return nil },
	},
	{
		id: optUnitFormat, long: "unit-format", value: valueRequired, arg: "<IEC|SI>",
		summary: "Unit format for sizes and rates (default: IEC)",
		parse: func(inv *invocation, v string) error {
			switch strings.ToLower(v) {
			case "si":
				inv.cfg.UnitFormat = config.UnitFormatSI
			case "iec":
				inv.cfg.UnitFormat = config.UnitFormatIEC
			default:
				return usagef("invalid value for --unit-format: %q", v)
			}
			return nil
		},
	},
	{
		id: optRetries, long: "retries", value: valueRequired, arg: "<n>",
		summary: "Maximum number of retries on a failed request (default: 3)",
		parse:   func(inv *invocation, v string) error { return setNonNegative(&inv.cfg.Retries, v, "--retries") },
	},
	{
		id: optWait, long: "wait", value: valueRequired, arg: "<milliseconds>",
		summary: "Time to wait between requests (default: 0)",
		parse:   func(inv *invocation, v string) error { return setNonNegative(&inv.cfg.Wait, v, "--wait") },
	},
	{
		id: optTimeout, long: "timeout", value: valueRequired, arg: "<seconds>",
		summary: "Connection timeout in seconds (default: 10)",
		parse: func(inv *invocation, v string) error {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				return usagef("invalid value for --timeout: %q", v)
			}
			inv.cfg.Curl.Timeout = int64(n)
			return nil
		},
	},

	{
		id: optDirectory, long: "directory", value: valueRequired, arg: "<path>",
		summary: "Download and installation root (default: .)",
		parse: func(inv *invocation, v string) error {
			inv.cfg.Directories.Directory = v
			return nil
		},
	},
	{
		id: optInstallDir, long: "install-dir", value: valueRequired, arg: "<name>",
		summary: "Subdirectory to install the game into (default: the manifest's installation directory)",
		parse: func(inv *invocation, v string) error {
			// The user gives a concrete directory name. The internal
			// subdirectory resolver still has a template language (the default
			// "%install_dir%" comes from the config), and accepting that
			// language here would hand users a half-exposed internal API, so
			// placeholders are refused rather than interpreted (review §5,
			// constraint A).
			if strings.ContainsRune(v, '%') {
				return usagef("--install-dir takes a directory name, not a template (%q)", v)
			}
			inv.cfg.Directories.GalaxyInstallSubdir = v
			return nil
		},
	},
	{
		id: optNoSubdirectories, long: "no-subdirectories",
		summary: "Install directly into the root, without a per-game subdirectory",
		parse: func(inv *invocation, _ string) error {
			inv.cfg.Directories.SubDirectories = false
			return nil
		},
	},
	{
		id: optPlatform, long: "platform", value: valueRequired, arg: "<windows|linux|mac>",
		summary: "Platform whose build is installed",
		parse: func(inv *invocation, v string) error {
			mask := util.OptionValue(v, config.Platforms, true)
			if mask == 0 {
				return usagef("invalid value for --platform: %q", v)
			}
			inv.cfg.DownloadConfig.GalaxyPlatform = mask
			return nil
		},
	},
	{
		id: optLanguage, long: "language", value: valueRequired, arg: "<language>",
		summary: "Language of the build that is installed (default: en)",
		parse: func(inv *invocation, v string) error {
			// An unmatched value leaves 0, which the Galaxy layer reads as
			// English — the upstream behaviour (downloader.cpp:3904-3913).
			inv.cfg.DownloadConfig.GalaxyLanguage = util.OptionValue(v, config.Languages, true)
			return nil
		},
	},
	{
		id: optArch, long: "arch", value: valueRequired, arg: "<x64|x86>",
		summary: "Architecture of the build that is installed (default: x64)",
		parse: func(inv *invocation, v string) error {
			arch := util.OptionValue(v, config.GalaxyArchs, false)
			if arch == 0 || arch == util.OptionValue("all", config.GalaxyArchs, false) {
				arch = config.ArchX64
			}
			inv.cfg.DownloadConfig.GalaxyArch = arch
			return nil
		},
	},

	{
		id: optThreads, long: "threads", value: valueRequired, arg: "<n>",
		summary: "Number of download workers",
		parse: func(inv *invocation, v string) error {
			n, err := strconv.ParseUint(v, 10, 32)
			if err != nil {
				return usagef("invalid value for --threads: %q", v)
			}
			// 0 keeps its meaning of "let the runtime fall back" (review D9);
			// it is not a second spelling of "auto".
			inv.cfg.Threads = uint32(n)
			return nil
		},
	},
	{
		id: optProgressInterval, long: "progress-interval", value: valueRequired, arg: "<ms>",
		summary: "Interval for progress bar updates, 1..10000 (default: 100)",
		parse: func(inv *invocation, v string) error {
			n, err := strconv.Atoi(v)
			if err != nil {
				return usagef("invalid value for --progress-interval: %q", v)
			}
			// Clamp, the way the upstream front end does (main.cpp:519-523):
			// an out-of-range value is bounded, never silently replaced by the
			// default.
			switch {
			case n < progressIntervalMin:
				n = progressIntervalMin
			case n > progressIntervalMax:
				n = progressIntervalMax
			}
			inv.cfg.ProgressInterval = n
			return nil
		},
	},
	{
		id: optCDNPriority, long: "cdn-priority", value: valueRequired, arg: "<a,b>",
		summary: "Galaxy CDN priority order",
		parse: func(inv *invocation, v string) error {
			inv.cfg.DownloadConfig.GalaxyCDNPriority = util.Split(v, ",")
			return nil
		},
	},
	{
		id: optNoDependencies, long: "no-dependencies",
		summary: "Do not install dependencies",
		parse: func(inv *invocation, _ string) error {
			inv.cfg.DownloadConfig.GalaxyDependencies = false
			return nil
		},
	},
	{
		id: optCheckFreeSpace, long: "check-free-space",
		summary: "Check for free space before installing",
		parse: func(inv *invocation, _ string) error {
			inv.cfg.DownloadConfig.FreeSpaceCheck = true
			return nil
		},
	},

	{
		id: optTag, long: "tag", value: valueRequired, arg: "<a,b>",
		summary: "Only games carrying these tags",
		parse: func(inv *invocation, v string) error {
			inv.cfg.DownloadConfig.Tags = util.Split(v, ",")
			return nil
		},
	},
	{
		id: optGame, long: "game", value: valueRequired, arg: "<regex>",
		summary: "Filter games by name (Perl regular expression)",
		parse: func(inv *invocation, v string) error {
			inv.cfg.GameRegex = v
			return nil
		},
	},
	{
		id: optGameList, long: "game-list", value: valueRequired, arg: "<file>",
		summary: "Filter games with the regular expressions in this file",
		parse: func(inv *invocation, v string) error {
			inv.cfg.GameListFilePath = v
			return nil
		},
	},
	{
		id: optUpdated, long: "updated",
		summary: "Only games that have an update flag",
		parse:   func(inv *invocation, _ string) error { inv.cfg.Updated = true; return nil },
	},
	{
		id: optNew, long: "new",
		summary: "Only games that have a new flag",
		parse:   func(inv *invocation, _ string) error { inv.cfg.New = true; return nil },
	},
	{
		id: optIncludeHidden, long: "include-hidden-products",
		summary: "Include games hidden on the account page",
		parse:   func(inv *invocation, _ string) error { inv.cfg.IncludeHiddenProducts = true; return nil },
	},
	{
		id: optIgnoreDLCCount, long: "ignore-dlc-count", value: valueOptional, arg: "[regex]",
		summary: "Ignore DLC counts for games matching the regular expression (default: .*)",
		parse: func(inv *invocation, v string) error {
			if v == "" {
				v = ".*"
			}
			inv.cfg.IgnoreDLCCountRegex = v
			return nil
		},
	},
	{
		id: optInstallerPlatform, long: "installer-platform", value: valueRequired, arg: "<spec>",
		summary: "Installer platform priority, e.g. windows,linux+mac (default: w+l)",
		parse: func(inv *invocation, v string) error {
			priority, mask := util.ParseOptionString(v, config.Platforms)
			if mask == 0 {
				return usagef("invalid value for --installer-platform: %q", v)
			}
			inv.cfg.PlatformPriority = v
			inv.cfg.DownloadConfig.PlatformPriority = priority
			inv.cfg.DownloadConfig.InstallerPlatform = mask
			return nil
		},
	},
	{
		id: optInstallerLanguage, long: "installer-language", value: valueRequired, arg: "<spec>",
		summary: "Installer language priority (default: en)",
		parse: func(inv *invocation, v string) error {
			priority, mask := util.ParseOptionString(v, config.Languages)
			if mask == 0 {
				return usagef("invalid value for --installer-language: %q", v)
			}
			inv.cfg.LanguagePriority = v
			inv.cfg.DownloadConfig.LanguagePriority = priority
			inv.cfg.DownloadConfig.InstallerLanguage = mask
			return nil
		},
	},
	// include and exclude are applied together after the loop, because exclude
	// removes bits from the include mask.
	{id: optInclude, long: "include", value: valueRequired, arg: "<spec>", summary: "What to install or list (default: all)"},
	{id: optExclude, long: "exclude", value: valueRequired, arg: "<spec>", summary: "What not to install or list"},
	{
		id: optBlacklist, long: "blacklist", value: valueRequired, arg: "<path>",
		summary: "File listing paths that must be ignored or deleted",
		parse: func(inv *invocation, v string) error {
			inv.cfg.BlacklistFilePath = v
			return nil
		},
	},
	{
		id: optIgnorelist, long: "ignorelist", value: valueRequired, arg: "<path>",
		summary: "File listing paths the orphan walk must skip",
		parse: func(inv *invocation, v string) error {
			inv.cfg.IgnorelistFilePath = v
			return nil
		},
	},

	{
		id: optSort, long: "sort", value: valueRequired, arg: "<date|score|none>",
		summary: "Build sorting order (default: score)",
		parse: func(inv *invocation, v string) error {
			// Stored unvalidated, as upstream does: an unknown order simply
			// does not reorder anything (downloader.cpp:6826-6830).
			inv.cfg.GalaxyBuildSortingOrder = v
			return nil
		},
	},

	{
		id: optYes, long: "yes",
		summary: "Do not ask for confirmation before deleting",
		parse:   func(inv *invocation, _ string) error { inv.yes = true; return nil },
	},

	{
		id: optBrowser, long: "browser",
		summary: "Force the browser-based login flow",
		parse:   func(inv *invocation, _ string) error { inv.cfg.ForceBrowserLogin = true; return nil },
	},
	{
		id: optEmail, long: "email", value: valueRequired, arg: "<address>",
		summary: "Account e-mail address (the password is asked for interactively)",
		parse: func(inv *invocation, v string) error {
			inv.cfg.Email = v
			return nil
		},
	},
}

// The parser keeps the two option name shapes apart.
//
// A long option is "--name" and a short one is "-x", where x is one of the
// declared single-letter aliases. Looking each shape up in its own table is what
// makes "-version", "-threads 8" and "--h" unknown options instead of accepted
// spellings: folding the prefixes together (or stripping every leading dash)
// would quietly widen the grammar (review S1-R1).
var (
	longByName  = map[string]*optionSpec{}
	shortByName = map[string]*optionSpec{}
)

func init() {
	for i := range optionTable {
		spec := &optionTable[i]
		longByName[spec.long] = spec
		for _, alias := range spec.aliases {
			shortByName[alias] = spec
		}
	}
}

// splitOption separates "name=value" into its parts; a token without "=" leaves
// hasValue false.
func splitOption(token string) (name, value string, hasValue bool) {
	if eq := strings.IndexByte(token, '='); eq >= 0 {
		return token[:eq], token[eq+1:], true
	}
	return token, "", false
}

// removedOptions maps an option this CLI deliberately dropped onto the command
// that replaced its behaviour.
//
// It exists for one reason: the diagnosis. The parser still fails on the option
// (unknown option, exit 2) — nothing is translated, nothing is accepted twice
// (review §11) — but a user following older documentation gets told where the
// capability went instead of guessing.
var removedOptions = map[string]string{
	"login":                  "goggo auth login",
	"browser-login":          "goggo auth login --browser",
	"check-login-status":     "goggo auth status",
	"logout":                 "goggo auth logout",
	"login-email":            "goggo auth login --email <address>",
	"login-password":         "goggo auth login (the password is asked for interactively)",
	"galaxy-install":         "goggo install <game>",
	"galaxy-show-builds":     "goggo show builds <game>",
	"galaxy-list-cdns":       "goggo show cdns <game>",
	"status":                 "goggo verify <game>",
	"check-orphans":          "goggo orphans check <game>",
	"delete-orphans":         "goggo orphans remove <game>",
	"subdir-galaxy-install":  "goggo install <game> --install-dir <name>",
	"galaxy-platform":        "goggo install <game> --platform windows",
	"galaxy-language":        "goggo install <game> --language en",
	"galaxy-arch":            "goggo install <game> --arch x64",
	"galaxy-cdn-priority":    "goggo install <game> --cdn-priority <a,b>",
	"galaxy-no-dependencies": "goggo install <game> --no-dependencies",
	"galaxy-builds-sort":     "goggo show builds <game> --sort <order>",
	"platform":               "goggo list games --installer-platform <spec>",
	"language":               "goggo list games --installer-language <spec>",
	"list":                   "goggo list games (also: list tags, list wishlist)",
	"tags":                   "goggo list games --tag <a,b>",
	"tag":                    "goggo list games --tag <a,b>",
	"verbosity":              "goggo -v (or --verbose)",
}

// usageError marks an argument or usage failure.
//
// It is the parser's single failure shape: the caller maps it onto the usage
// exit code (2), so "the user asked for something the CLI does not offer" is
// never mistaken for "the operation failed" (review D2/D3).
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

func usagef(format string, args ...any) error {
	return &usageError{msg: fmt.Sprintf(format, args...)}
}

// isUsageError reports whether err is a usage failure.
func isUsageError(err error) bool {
	var u *usageError
	return errors.As(err, &u)
}

// parseArgs parses a command line into an invocation.
//
// Options may appear anywhere on the line; the first bare token starts the
// command path, and the tokens after it are subcommands while the tree has a
// matching child, then the command's arguments (review §6). The parse is
// deliberately two-phase: the command path is resolved first, and only then are
// the options checked against what that command accepts (D15) — so
// "goggo list --threads 8" fails as an unaccepted option rather than being
// quietly ignored.
func parseArgs(args []string, cfg config.Config) (invocation, error) {
	inv := invocation{cfg: cfg}
	applyParseDefaults(&inv.cfg)

	var (
		words                    []string
		used                     []optionID
		helpSeen, versionSeen    bool
		includeSeen, excludeSeen string
		includeSet, excludeSet   bool
	)

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "" || arg == "-" || !strings.HasPrefix(arg, "-") {
			words = append(words, arg)
			continue
		}

		var (
			spec     *optionSpec
			name     string
			value    string
			hasValue bool
		)
		switch {
		case strings.HasPrefix(arg, "--"):
			// "--" alone is not an option: there is no end-of-options marker in
			// this CLI, and silently accepting it would invent one.
			name, value, hasValue = splitOption(arg[2:])
			if name == "" {
				return invocation{}, usagef("unexpected argument %q", arg)
			}
			spec = longByName[name]
		default:
			// A single dash introduces exactly one letter, and only the letters
			// the table declares: "-threads" is not a long option spelled
			// short, it is nothing.
			token := arg[1:]
			if len(token) != 1 {
				return invocation{}, unknownOption(token)
			}
			name = token
			spec = shortByName[name]
		}
		if spec == nil {
			return invocation{}, unknownOption(name)
		}
		switch spec.value {
		case valueNone:
			if hasValue {
				return invocation{}, usagef("option --%s does not take a value", name)
			}
		case valueRequired:
			if !hasValue {
				if i+1 >= len(args) {
					return invocation{}, usagef("option --%s requires a value", name)
				}
				i++
				value = args[i]
			}
		case valueOptional:
			if !hasValue && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				value = args[i]
			}
		}
		switch spec.id {
		case optHelp:
			helpSeen = true
			continue
		case optVersion:
			versionSeen = true
			continue
		case optInclude:
			includeSeen, includeSet = value, true
			used = append(used, spec.id)
			continue
		case optExclude:
			excludeSeen, excludeSet = value, true
			used = append(used, spec.id)
			continue
		}
		if spec.parse != nil {
			if err := spec.parse(&inv, value); err != nil {
				return invocation{}, err
			}
		}
		used = append(used, spec.id)
	}

	node, path, rest, err := resolveCommand(words)
	if err != nil {
		return invocation{}, err
	}

	// The meta shortcuts answer before the command's own arity is checked, so
	// "goggo install -h" is help rather than a missing-argument error (D18).
	if helpSeen {
		inv.meta = metaHelp
		inv.helpPath = path
		return inv, nil
	}
	if versionSeen {
		inv.meta = metaVersion
		return inv, nil
	}
	if node.name == "help" {
		inv.meta = metaHelp
		inv.helpPath = rest
		return inv, nil
	}
	if node.name == "version" {
		inv.meta = metaVersion
		if len(rest) != 0 {
			return invocation{}, usagef("version takes no arguments")
		}
		return inv, nil
	}

	if node.name == "" {
		// No command on the line: nothing to check the options against, and
		// nothing to run. The caller decides what a bare invocation prints.
		inv.cmd = cmdNone
		return inv, nil
	}

	// Options are checked against the command that will run.
	for _, id := range used {
		if !node.accepts(id) {
			return invocation{}, usagef("option --%s is not accepted by %s", optionName(id), strings.Join(path, " "))
		}
	}

	// include/exclude combine into the one mask the domain consumes.
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
		inv.cfg.DownloadConfig.Include = inc &^ exc
	}

	want, takes := commandArity(node.id)
	switch {
	case !takes && len(rest) != 0:
		return invocation{}, usagef("%s takes no arguments", strings.Join(path, " "))
	case takes && len(rest) == 0:
		return invocation{}, usagef("%s needs a %s", strings.Join(path, " "), want)
	case takes && len(rest) != 1:
		return invocation{}, usagef("%s takes one %s, got %d", strings.Join(path, " "), want, len(rest))
	case takes:
		tgt, err := parseTarget(rest[0])
		if err != nil {
			return invocation{}, err
		}
		inv.target = tgt
	}

	// "show builds" lists the builds of a product; a build in the argument is
	// not a filter there, it is a different command (show manifest).
	if node.id == cmdShowBuilds && inv.target.Build != "" {
		return invocation{}, usagef("show builds takes a game, not a build (use show manifest %s)", rest[0])
	}

	inv.cmd = node.id
	// Directory arguments are normalised once parsing is over: an empty value
	// means the current directory, and any other value ends in a separator.
	inv.cfg.Directories.Directory = ensureTrailingSlash(inv.cfg.Directories.Directory, defaultDirectory)
	return inv, nil
}

// metaCommands are the built-ins the tree accepts without a command id (D18).
var metaCommands = []commandNode{
	{name: "help", summary: "Show help for a command"},
	{name: "version", summary: "Show version"},
}

// resolveCommand walks the words against the tree and returns the node, the
// matched path, the leftover arguments and a usage error when the line cannot be
// resolved (unknown command, namespace without a subcommand, unknown
// subcommand).
func resolveCommand(words []string) (commandNode, []string, []string, error) {
	if len(words) == 0 {
		return commandNode{}, nil, nil, nil
	}
	nodes := append(append([]commandNode{}, commandTree...), metaCommands...)
	var (
		node commandNode
		path []string
		idx  int
	)
	for idx < len(words) {
		child, ok := findChild(nodes, words[idx])
		if !ok {
			break
		}
		node = child
		path = append(path, child.name)
		idx++
		if len(child.children) == 0 {
			break
		}
		nodes = child.children
	}
	if len(path) == 0 {
		return commandNode{}, nil, nil, unknownCommand(words[0])
	}
	if len(node.children) != 0 {
		// The path stopped inside a namespace.
		if idx == len(words) {
			return commandNode{}, nil, nil, usagef("command %q needs a subcommand (%s)", strings.Join(path, " "), childNames(node))
		}
		return commandNode{}, nil, nil, usagef("unknown subcommand %q for %q (%s)", words[idx], strings.Join(path, " "), childNames(node))
	}
	return node, path, words[idx:], nil
}

func childNames(node commandNode) string {
	names := make([]string, 0, len(node.children))
	for _, c := range node.children {
		names = append(names, c.name)
	}
	return strings.Join(names, ", ")
}

// commandArity says what a command takes after its path: the argument's name,
// or false when it takes none.
func commandArity(id commandID) (string, bool) {
	switch id {
	case cmdInstall, cmdVerify, cmdShowBuilds, cmdShowManifest, cmdShowCDNs,
		cmdOrphansCheck, cmdOrphansRemove:
		return "game", true
	}
	return "", false
}

// parseTarget splits "<product id or gamename>[/<build id or index>]".
//
// The split is done here rather than through util.Split: that helper mirrors the
// upstream tokenizer and DROPS empty tokens, which is exactly what would hide a
// malformed argument ("/2" would silently become the game "2"). The shape is
// therefore checked on the raw string. More than two parts, or an empty game, is
// a usage error rather than a silently ignored tail (D2) — the C++ front end
// ignored the remainder; this CLI refuses what it does not understand. An empty
// build is read as "no build given", the way the upstream tokenizer reads it.
func parseTarget(arg string) (target, error) {
	parts := strings.SplitN(arg, "/", 3)
	if len(parts) > 2 {
		return target{}, usagef("invalid target %q: expected <game>[/<build>]", arg)
	}
	if parts[0] == "" {
		return target{}, usagef("invalid target %q: the game is empty", arg)
	}
	tgt := target{Product: parts[0]}
	if len(parts) == 2 {
		tgt.Build = parts[1]
	}
	return tgt, nil
}

// unknownOption builds the failure for an option this CLI does not have, adding
// the migration hint when the option is one of the upstream flags the redesign
// removed.
func unknownOption(name string) error {
	if suggestion, ok := removedOptions[name]; ok && suggestion != "" {
		return usagef("unknown option --%s\nhint: use '%s'", name, suggestion)
	}
	return usagef("unknown option --%s", name)
}

// unknownCommand builds the failure for a verb outside the tree, with the same
// migration hint for the upstream commands that became something else.
func unknownCommand(name string) error {
	if suggestion, ok := removedOptions[name]; ok && suggestion != "" {
		return usagef("unknown command %q\nhint: use '%s'", name, suggestion)
	}
	return usagef("unknown command %q", name)
}

// optionName is the option's canonical long name, for diagnostics.
func optionName(id optionID) string {
	for i := range optionTable {
		if optionTable[i].id == id {
			return optionTable[i].long
		}
	}
	return "?"
}

// applyParseDefaults installs the defaults the option parser owns.
//
// They are the values the parser itself declares — the same set the previous
// front end installed, minus the two that are decisions of their own: the
// download worker count is settled by the threads benchmark (D9), and the
// progress interval keeps the renderer's own 100 ms fallback until then.
func applyParseDefaults(cfg *config.Config) {
	cfg.GalaxyBuildSortingOrder = defaultGalaxyBuildSort
	cfg.DownloadConfig.GalaxyPlatform = util.OptionValue(defaultGalaxyPlatform, config.Platforms, true)
	cfg.DownloadConfig.GalaxyLanguage = util.OptionValue(defaultGalaxyLanguage, config.Languages, true)
	cfg.DownloadConfig.GalaxyArch = util.OptionValue(defaultGalaxyArch, config.GalaxyArchs, false)
	cfg.DownloadConfig.GalaxyCDNPriority = util.Split(defaultGalaxyCDNPriority, ",")
	cfg.Directories.GalaxyInstallSubdir = defaultGalaxyInstallSubdir
	// --no-subdirectories and --no-dependencies are negations of their
	// settings, so the parser's default is the positive value.
	cfg.Directories.SubDirectories = true
	cfg.DownloadConfig.GalaxyDependencies = true
}

// setNonNegative parses an integer that may not be negative.
func setNonNegative(dst *int, value, option string) error {
	n, err := strconv.Atoi(value)
	if err != nil || n < 0 {
		return usagef("invalid value for %s: %q", option, value)
	}
	*dst = n
	return nil
}
