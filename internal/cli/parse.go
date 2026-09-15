package cli

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/nekrozis/goggo/internal/config"
	"github.com/nekrozis/goggo/internal/core"
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

	// website subdirectory layout (GD4, the download commands only). The
	// whitelist for each lives in config.SubdirOptions.
	optSubdirInstallers
	optSubdirExtras
	optSubdirPatches
	optSubdirLanguagePacks
	optSubdirDLC
	optSubdirGame

	// download file only.
	optOutputFile

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
	// detail is the longer explanation a command topic prints under the
	// option. Empty means the summary says everything.
	detail string
	parse  func(inv *invocation, value string) error
}

// optionTable is the CLI's complete option vocabulary (CLI1 v8 §5). The six
// website subdirectory options are generated from config.SubdirOptions so the
// whitelist, the defaults and the help text cannot drift from that table.
var optionTable = append([]optionSpec{
	{id: optHelp, long: "help", aliases: []string{"h"}, summary: "Show help"},
	{id: optVersion, long: "version", summary: "Show version"},
	{
		id: optVerbose, long: "verbose", aliases: []string{"v"},
		summary: "Verbose output (per-file records, skipped files)",
		detail: "Adds the per-file records the default output aggregates:\n" +
			"skipped files, container members and orphan paths.",
		parse: func(inv *invocation, _ string) error { inv.cfg.MsgLevel = msgLevelVerbose; return nil },
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
		detail: "The root every path is built from: the game goes into\n" +
			"<directory>/<install-dir>, and the account data lives beside it.",
		parse: func(inv *invocation, v string) error {
			inv.cfg.Directories.Directory = v
			return nil
		},
	},
	{
		id: optInstallDir, long: "install-dir", value: valueRequired, arg: "<name>",
		summary: "Subdirectory to install the game into (default: the manifest's installation directory)",
		detail: "A concrete directory name, or one of the installation templates:\n" +
			strings.Join(core.InstallSubdirTemplates, "\n") + "\n" +
			"A template is matched whole: it is not expanded inside a longer path.",
		parse: func(inv *invocation, v string) error {
			// A value carrying a placeholder must be one of the templates the
			// installer actually resolves. The list comes from core so the
			// whitelist cannot drift from the resolver (review GD3 §3.3);
			// anything else with a "%" in it would be a half-exposed template
			// language, and the resolver would keep it as a literal directory
			// name (review CLI1 §5, constraint A).
			if strings.ContainsRune(v, '%') && !core.IsInstallSubdirTemplate(v) {
				return usagef("--install-dir takes a directory name or one of the known templates (%q)", v)
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
		summary: "Number of download workers (default: 8)",
		detail:  "0 is not \"auto\": it falls back to a single worker at run time.",
		parse: func(inv *invocation, v string) error {
			n, err := strconv.ParseUint(v, 10, 32)
			if err != nil {
				return usagef("invalid value for --threads: %q", v)
			}
			// An explicit value always wins over the parser's default, and 0
			// keeps its meaning of "let the runtime fall back" (review D9/D46);
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
		detail:  "Comma-separated; a game must carry every listed tag.",
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
		detail: "Required when stdin is not a terminal: this CLI never deletes\n" +
			"without either a confirmation or this flag.",
		parse: func(inv *invocation, _ string) error { inv.yes = true; return nil },
	},

	{
		id: optBrowser, long: "browser",
		summary: "Force the browser-based login flow",
		parse:   func(inv *invocation, _ string) error { inv.cfg.ForceBrowserLogin = true; return nil },
	},
	{
		id: optOutputFile, long: "output-file", aliases: []string{"o"},
		value: valueRequired, arg: "<path>",
		summary: "Output file name for a single download file spec",
		detail: "Only valid with exactly one spec; with several specs it is a\n" +
			"usage error, and it must not name an existing directory.",
		parse: func(inv *invocation, v string) error {
			inv.outputFile = v
			return nil
		},
	},
	{
		id: optEmail, long: "email", value: valueRequired, arg: "<address>",
		summary: "Account e-mail address (the password is asked for interactively)",
		parse: func(inv *invocation, v string) error {
			inv.cfg.Email = v
			return nil
		},
	},
}, subdirOptionSpecs()...)

// subdirOptionIDs maps each config.SubdirOptions name onto its option id.
var subdirOptionIDs = map[string]optionID{
	"installers":     optSubdirInstallers,
	"extras":         optSubdirExtras,
	"patches":        optSubdirPatches,
	"language-packs": optSubdirLanguagePacks,
	"dlc":            optSubdirDLC,
	"game":           optSubdirGame,
}

// subdirOptionSpecs generates the six --subdir-* options from the config
// table: same long names, same defaults (declared in the summary), same
// per-field whole-template whitelist (GD4 plan 3.3). The defaults themselves
// are applied by applyParseDefaults, which reads the same table.
func subdirOptionSpecs() []optionSpec {
	specs := make([]optionSpec, 0, len(config.SubdirOptions))
	for _, opt := range config.SubdirOptions {
		opt := opt
		id, ok := subdirOptionIDs[opt.Name]
		if !ok {
			panic("cli: config.SubdirOptions carries a name the option table does not declare: " + opt.Name)
		}
		def := opt.Default
		if def == "" {
			def = "none"
		}
		templates := strings.Join(opt.Templates, "\n")
		specs = append(specs, optionSpec{
			id:      id,
			long:    "subdir-" + opt.Name,
			value:   valueRequired,
			arg:     "<name>",
			summary: "Subdirectory for " + opt.Name + " (default: " + def + ")",
			detail: "A concrete directory name, or one of the whole templates:\n" +
				templates + "\n" +
				"A template is matched whole: it is not expanded inside a longer path.",
			parse: func(inv *invocation, v string) error {
				// A value carrying a placeholder must be one of the whole
				// templates this field renders; anything else with a "%" in
				// it is a half-exposed template language (review GD3 3.3,
				// same rule as --install-dir, own table per GD4 ruling 3).
				if !config.SubdirValueAccepted(opt, v) {
					return usagef("--subdir-%s takes a directory name or one of the known templates (%q)", opt.Name, v)
				}
				opt.Set(&inv.cfg.Directories, v)
				return nil
			},
		})
	}
	return specs
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
	"download":               "goggo download <game>",
	"download-file":          "goggo download file <game>/<fileid>",
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

	// The help shortcut answers BEFORE the command line is resolved: it asks
	// about a topic, so it must not require a runnable command underneath
	// ("goggo auth -h" is help for the auth namespace, not a
	// missing-subcommand error) and must not check the command's arity (D18).
	// Both spellings — this shortcut and the help command — resolve their topic
	// with the SAME rule, so they can never disagree about what a topic is
	// (review S2: the two used to differ on unknown and leftover words).
	if helpSeen {
		topic, err := resolveHelpTopic(words)
		if err != nil {
			return invocation{}, err
		}
		inv.meta = metaHelp
		inv.helpPath = topic
		return inv, nil
	}
	if versionSeen {
		inv.meta = metaVersion
		return inv, nil
	}

	node, path, rest, err := resolveCommand(words)
	if err != nil {
		return invocation{}, err
	}

	if node.name == "help" {
		topic, err := resolveHelpTopic(rest)
		if err != nil {
			return invocation{}, err
		}
		inv.meta = metaHelp
		inv.helpPath = topic
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

	argName, count := commandArity(node.id)
	switch {
	case count == 0 && len(rest) != 0:
		return invocation{}, usagef("%s takes no arguments", strings.Join(path, " "))
	case count != 0 && len(rest) == 0:
		return invocation{}, usagef("%s needs a %s", strings.Join(path, " "), argName)
	case count == 1 && len(rest) != 1:
		return invocation{}, usagef("%s takes one %s, got %d", strings.Join(path, " "), argName, len(rest))
	case count == 1:
		tgt, err := parseTarget(rest[0])
		if err != nil {
			return invocation{}, err
		}
		inv.target = tgt
	case count < 0:
		inv.args = append([]string{}, rest...)
	}

	// A batch game is a name, never a spec: a slash means the user meant the
	// subcommand, and guessing which half is a file id is not the parser's to do.
	if node.id == cmdDownload {
		for _, game := range inv.args {
			if strings.Contains(game, "/") {
				return invocation{}, usagef("download takes game names; to fetch one file use %q", "download file "+game+"/<fileid>")
			}
		}
	}

	// -o names one output file, so it belongs to exactly one spec. Upstream
	// refuses it at dispatch (main.cpp:563); this CLI refuses it where the
	// shape is known — the parser (GD4 Gate 1 ruling 5).
	if node.id == cmdDownloadFile && inv.outputFile != "" && len(inv.args) > 1 {
		return invocation{}, usagef("download file takes -o with exactly one spec, got %d", len(inv.args))
	}

	// "show builds" lists the builds of a product; a build in the argument is
	// not a filter there, it is a different command (show manifest).
	if node.id == cmdShowBuilds && inv.target.Build != "" {
		return invocation{}, usagef("show builds takes a game, not a build (use show manifest %s)", rest[0])
	}

	inv.cmd = node.id
	// The session class travels with the command: the dispatcher applies the
	// declaration instead of deciding it (review S4).
	inv.session = node.session
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
	if len(node.children) != 0 && node.id == cmdNone {
		// The path stopped inside a pure namespace.
		if idx == len(words) {
			return commandNode{}, nil, nil, usagef("command %q needs a subcommand (%s)", strings.Join(path, " "), childNames(node))
		}
		return commandNode{}, nil, nil, usagef("unknown subcommand %q for %q (%s)", words[idx], strings.Join(path, " "), childNames(node))
	}
	// A node that is both leaf and namespace ("download") that got here with
	// a first word no child matched dispatches as the leaf: the leftover
	// words are its arguments (GD4 ruling 9).
	return node, path, words[idx:], nil
}

func childNames(node commandNode) string {
	names := make([]string, 0, len(node.children))
	for _, c := range node.children {
		names = append(names, c.name)
	}
	return strings.Join(names, ", ")
}

// commandArity says what a command takes after its path: the argument's name
// and how many — 0 for none, 1 for exactly one, -1 for one or more (GD4's
// download commands are variadic; the rest keep CLI1's single target).
func commandArity(id commandID) (string, int) {
	switch id {
	case cmdInstall, cmdVerify, cmdShowBuilds, cmdShowManifest, cmdShowCDNs,
		cmdOrphansCheck, cmdOrphansRemove:
		return "game", 1
	case cmdDownload:
		return "game", -1
	case cmdDownloadFile:
		return "spec", -1
	}
	return "", 0
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

// resolveHelpTopic turns the words after a help request into a topic path.
//
// One rule serves both spellings (review S3):
//
//	no words                  → the root topic
//	longest resolvable prefix → that node's topic
//	pure namespace with leftovers → usage error: unknown subcommand
//	leaf with at most one argument → its topic (help does NOT require the
//	                                  argument the command would need to run)
//	leaf with more leftovers  → usage error, except the variadic download
//	                           commands, which take one or more
//	nothing resolvable        → usage error: unknown command
//
// The alternative — printing the root help for anything unrecognised — would
// answer a question the user did not ask while looking like success, which is
// exactly what D2 refuses.
func resolveHelpTopic(words []string) ([]string, error) {
	if len(words) == 0 {
		return nil, nil
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
		return nil, unknownCommand(words[0])
	}
	if len(node.children) != 0 && node.id == cmdNone {
		if idx < len(words) {
			return nil, usagef("unknown subcommand %q for %q (%s)", words[idx], strings.Join(path, " "), childNames(node))
		}
		return path, nil
	}
	if want, count := commandArity(node.id); count != 0 {
		if count == 1 && len(words)-idx > 1 {
			return nil, usagef("%s takes one %s, got %d", strings.Join(path, " "), want, len(words)-idx)
		}
		return path, nil
	}
	if len(words) != idx {
		return nil, usagef("%s takes no arguments", strings.Join(path, " "))
	}
	return path, nil
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
// They are the values the parser itself declares. One of them is a decision of
// its own rather than an inherited value: the download worker count, settled by
// measurement (D9/D46) and documented on defaultThreads.
//
// This runs before any option is read, so an option on the command line simply
// overwrites what is set here — including "--threads 0", which keeps its meaning
// of "let the runtime fall back" instead of becoming a second spelling of this
// default.
func applyParseDefaults(cfg *config.Config) {
	cfg.GalaxyBuildSortingOrder = defaultGalaxyBuildSort
	cfg.DownloadConfig.GalaxyPlatform = util.OptionValue(defaultGalaxyPlatform, config.Platforms, true)
	cfg.DownloadConfig.GalaxyLanguage = util.OptionValue(defaultGalaxyLanguage, config.Languages, true)
	cfg.DownloadConfig.GalaxyArch = util.OptionValue(defaultGalaxyArch, config.GalaxyArchs, false)
	cfg.DownloadConfig.GalaxyCDNPriority = util.Split(defaultGalaxyCDNPriority, ",")
	// The installer platform/language the website conversion gates on.
	// Upstream declares them as front-end default_values ("w+l" and "en",
	// main.cpp:280-281) and parses each into the priority list AND the
	// installer mask (Util::parseOptionString, util.cpp:312). Leaving them
	// zero made every non-extras vector drop silently — GD4's download
	// commands are the conversion's first production consumers and exposed
	// it on the real account (audit GD4, DEFECT-GD4-1). The parser declares
	// the defaults from the same constants config.NewConfig installs, and
	// the --installer-platform/--installer-language closures are the single
	// override path.
	platformPriority, installerPlatform := util.ParseOptionString(config.DefaultPlatformPriority, config.Platforms)
	cfg.DownloadConfig.PlatformPriority = platformPriority
	cfg.DownloadConfig.InstallerPlatform = installerPlatform
	languagePriority, installerLanguage := util.ParseOptionString(config.DefaultLanguagePriority, config.Languages)
	cfg.DownloadConfig.LanguagePriority = languagePriority
	cfg.DownloadConfig.InstallerLanguage = installerLanguage
	// Remote XML is on unless --no-remote-xml says otherwise (main.cpp:282
	// and 541: bRemoteXML = !bNoRemoteXML, default false). With it off the
	// installer/patch version check silently disappears, so the parser
	// declares the upstream default (the option itself is not registered —
	// D14 keeps the surface to what is wired; the field is settable by a
	// future config layer).
	cfg.DownloadConfig.RemoteXML = true
	cfg.Directories.GalaxyInstallSubdir = defaultGalaxyInstallSubdir
	// The six website subdirectory defaults come from the config table —
	// the same single source the whitelist and the help text read (GD4 3.3).
	for _, opt := range config.SubdirOptions {
		opt.Set(&cfg.Directories, opt.Default)
	}
	cfg.Threads = defaultThreads
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
