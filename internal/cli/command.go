package cli

import (
	"strings"

	"github.com/nekrozis/goggo/internal/config"
)

// The command tree the CLI is built on: this CLI's own product surface,
// organised by user concept rather than by the upstream flag set.
//
// This file is the parser's data and vocabulary — which commands exist, how they
// nest, what each node accepts — and holds no behaviour. Only commands this build
// supports appear here: a verb that is not in the tree is absent, never
// present-and-unimplemented.

// sessionClass is what a command needs from the session before it can run, and
// whether it may create one.
//
// It is declared on the tree rather than decided in the dispatcher, so the login
// contract sits next to the command it governs and a new command cannot be added
// without stating it: the completeness test rejects a leaf that leaves the class
// unset.
type sessionClass uint8

const (
	// sessionUnset is not a usable class: it is what a leaf that forgot to
	// declare one has. Nothing may dispatch such a command.
	sessionUnset sessionClass = iota

	// sessionNone runs without a session, so a missing one is not a failure:
	// `auth status` answers "Not logged in" itself and `auth clear` only
	// removes local files.
	sessionNone

	// sessionRequired needs a usable session and never logs in: every
	// read-only command. A missing session fails the run with
	// core.ErrSessionRequired.
	sessionRequired

	// sessionImplicitLogin needs a session and may create one when a terminal
	// can answer the prompts: the commands that write. Without a terminal it
	// fails like sessionRequired — it must not reach the headless credentials
	// branch, whose diagnostic is written for an explicit login.
	sessionImplicitLogin

	// sessionExplicitLogin is the user asking for a login, so the flow always
	// runs, terminal or not: without one, stdin may still carry the browser
	// callback URL, and a missing credential is reported as such.
	sessionExplicitLogin
)

// String names the class for diagnostics; an undeclared class says so instead of
// printing a number.
func (c sessionClass) String() string {
	switch c {
	case sessionNone:
		return "sessionNone"
	case sessionRequired:
		return "sessionRequired"
	case sessionImplicitLogin:
		return "sessionImplicitLogin"
	case sessionExplicitLogin:
		return "sessionExplicitLogin"
	}
	return "sessionUnset"
}

// commandID identifies one leaf command: a node that can actually run.
//
// Namespaces (auth, list, galaxy, backup, orphans) have no id; they only group their
// children.
type commandID uint8

const (
	cmdNone commandID = iota

	cmdAuthLogin
	cmdAuthClear
	cmdAuthStatus

	cmdListGames
	cmdListTags
	cmdListWishlist

	cmdGame

	cmdGalaxyBuilds
	cmdGalaxyManifest
	cmdGalaxyCDNs

	cmdInstall
	cmdInstallOptions

	cmdVerify

	cmdBackupList
	cmdBackupDownload

	cmdOrphansCheck
	cmdOrphansRemove
)

// metaAction is what a meta invocation asks for. Meta commands are built in:
// they short-circuit before any business dispatch.
type metaAction uint8

const (
	metaNone metaAction = iota
	metaHelp
	metaVersion
)

// target is the "<product id or gamename>[/<build id or index>]" argument the
// Galaxy-backed commands take. Splitting it is the parser's job: the dispatcher
// receives the two parts, never the joined string.
type target struct {
	Product string
	Build   string
}

// invocation is what a parsed command line carries: the effective configuration
// plus the semantic payload of the resolved command. It deliberately holds no
// core types — an invocation says what the user asked for, not how it will be
// done.
type invocation struct {
	cfg config.Config

	meta     metaAction
	helpPath []string

	cmd commandID
	// session is the resolved command's session class, copied from its tree
	// node — never re-derived here, so the declaration the help shows and the
	// policy the dispatcher applies are the same value.
	session sessionClass
	target  target
	// args carries the one-or-more positional arguments of the variadic
	// commands (download, download file). The fixed-arity commands use
	// target instead; the two are never both filled.
	args []string
	// outputFile is the -o value of download file, kept raw: the parser
	// refuses it with several specs, and the dispatcher refuses a
	// directory.
	outputFile string
	// yes carries the destructive-confirmation flag. Only the destructive
	// commands accept it.
	yes bool
	// productRefRegex carries --regex: the positional product argument is read
	// as an expression instead of as a product's own name.
	productRefRegex bool
	// json carries --json: format command output as JSON.
	json bool
}

// commandNode is one node of the tree.
//
// options lists what the node ADDS to the shared set (sharedOptions): the parser
// checks an option against common ∪ node, so "global" means shared semantics, not
// "every command accepts it". id is cmdNone for pure namespaces; "download"
// is the one node that is both a leaf and a namespace — a first word matching a
// child dispatches the subcommand, anything else is an argument of the leaf.
//
// Every leaf declares a session class; namespaces and meta commands do not,
// because nothing dispatches them.
type commandNode struct {
	name     string
	summary  string
	id       commandID
	session  sessionClass
	options  []optionID
	children []commandNode
	// notes are the lines a reader must see before running the command: the
	// help prints them between the summary and the options.
	notes []string
}

// orphanNotes is the warning both orphan commands carry.
//
// Orphan detection uses the manifest of the selected platform and language, so
// files belonging to another variant can be reported as orphaned. A destructive
// command must say that before it runs, not only in a log.
var orphanNotes = []string{
	"Orphan detection uses the manifest of the selected platform and language:",
	"files belonging to other variants may be reported as orphaned.",
}

// The installation-locating options, shared by every command that has to
// resolve an install path.
//
// installPath is derived from all of these at once — the directory plus the
// resolved subdirectory template under the selected platform, language and arch
// — so a command that accepts only some of them would resolve a different root
// than its siblings and report on the wrong tree.
var installTargetOptions = []optionID{
	optDirectory,
	optInstallDir,
	optNoSubdirectories,
	optPlatform,
	optLanguage,
	optArch,
}

// productRefOptions is how the positional product argument is read, and every
// command that takes one accepts it. It is kept apart from the groups around it
// because it says nothing about where a product installs or which games a
// listing covers: it only changes the meaning of the argument beside it.
var productRefOptions = []optionID{optRegex}

// listGamesOptions filters the games listing.
//
// The installer filters are the listing side of platform/language. They are
// accepted by `list games` — the listing that shows installers — and not by the
// other list resources, where they would filter nothing.
var listGamesOptions = []optionID{
	optTag,
	optGame,
	optGameList,
	optUpdated,
	optNew,
	optIncludeHidden,
	optIgnoreDLCCount,
	optInstallerPlatform,
	optInstallerLanguage,
}

// saveOptions are the six artifact switches. On download they fetch AND write;
// on list details/json they gate the fetch and the display only — list never
// writes. download file accepts none of them: an option that does nothing is not
// offered.
var saveOptions = []optionID{
	optSaveSerials,
	optSaveChangelogs,
	optSaveLogo,
	optSaveIcon,
	optSaveGameDetailsJSON,
	optSaveProductJSON,
}

// detailsOptions are what the two list leaves accept: the account filters, the
// conversion mask, the blacklist the text renderer honours, the save flags as
// fetch/display gates, and the acquisition worker count.
var detailsOptions = joinOptions(listGamesOptions,
	[]optionID{optInclude, optExclude, optBlacklist},
	saveOptions,
	[]optionID{optInfoThreads},
	productRefOptions)

// listDetailsNotes is shared by both list leaves: the two facts a reader
// must have before trusting the output.
var listDetailsNotes = []string{
	"Read-only: this command writes no files and downloads nothing.",
	"Serials and changelog appear only with the matching --save-* flag,",
	"which gates whether they are fetched at all — not the display.",
}

// verifyOptions are what a read-only verification honours: it filters by the
// include mask and honours the blacklist, while the orphan walk does neither.
var verifyOptions = []optionID{
	optInclude,
	optExclude,
	optBlacklist,
}

// orphansOptions are the filter files the walk consults. include/exclude are
// deliberately absent: the walk never narrows by them.
var orphansOptions = []optionID{
	optIgnorelist,
	optBlacklist,
}

// subdirOptions are the six website subdirectory layout options. They belong to
// the download commands only: the install face resolves its own root through
// --install-dir, and these fill DirectoryConfig for MakeFilepaths.
var subdirOptions = []optionID{
	optSubdirInstallers,
	optSubdirExtras,
	optSubdirPatches,
	optSubdirLanguagePacks,
	optSubdirDLC,
	optSubdirGame,
}

// commandTree is the product surface: read it top to bottom and you have the
// CLI's complete vocabulary — and, on each leaf, whether the command needs a
// session and may log in.
var commandTree = []commandNode{
	{
		name:    "auth",
		summary: "Authentication",
		children: []commandNode{
			{name: "login", summary: "Log in", id: cmdAuthLogin,
				session: sessionExplicitLogin, options: []optionID{optBrowser, optEmail}},
			{name: "clear", summary: "Clear local login state", id: cmdAuthClear,
				session: sessionNone},
			{name: "status", summary: "Report the authentication state", id: cmdAuthStatus,
				session: sessionNone},
		},
	},
	{
		name:    "list",
		summary: "List account content",
		children: []commandNode{
			{name: "games", summary: "List owned games", id: cmdListGames,
				session: sessionRequired, options: listGamesOptions},
			{name: "tags", summary: "List tags", id: cmdListTags, session: sessionRequired},
			{name: "wishlist", summary: "List the wishlist", id: cmdListWishlist, session: sessionRequired},
		},
	},
	{
		name:    "game",
		summary: "Show game information",
		id:      cmdGame,
		session: sessionNone,
		options: joinOptions([]optionID{optJSON}, productRefOptions),
	},
	{
		name:    "galaxy",
		summary: "Inspect GOG Galaxy resources",
		children: []commandNode{
			{name: "builds", summary: "List a product's builds", id: cmdGalaxyBuilds,
				session: sessionRequired, options: []optionID{optSort, optRegex}},
			{name: "manifest", summary: "Show a build's manifest", id: cmdGalaxyManifest,
				session: sessionRequired, options: productRefOptions},
			{name: "cdns", summary: "List a build's CDN endpoints", id: cmdGalaxyCDNs,
				session: sessionRequired, options: productRefOptions},
		},
	},
	{
		name:    "install",
		summary: "Make the local installation match the manifest",
		id:      cmdInstall,
		session: sessionImplicitLogin,
		options: joinOptions(installTargetOptions, productRefOptions, []optionID{
			optThreads,
			optProgressInterval,
			optCDNPriority,
			optNoDependencies,
			optCheckFreeSpace,
		}),
		children: []commandNode{
			{
				name:    "options",
				summary: "List available installation options",
				id:      cmdInstallOptions,
				session: sessionRequired,
				options: productRefOptions,
			},
		},
	},
	{
		name:    "verify",
		summary: "Report whether the local files match the manifest",
		id:      cmdVerify,
		session: sessionRequired,
		options: joinOptions(installTargetOptions, productRefOptions, verifyOptions),
		// The report's vocabulary is the status codes, so the topic has to
		// define them; and a verification never repairs, which a reader has
		// to know before relying on it.
		notes: []string{
			"Status codes: OK (matches), ND (not downloaded), MD5 (content differs),",
			"FS (size differs). Nothing is repaired: a mismatch is reported, never fixed.",
		},
	},
	{
		name:    "orphans",
		summary: "Files in the installation that no manifest accounts for",
		children: []commandNode{
			{name: "check", summary: "List them (read-only)", id: cmdOrphansCheck,
				session: sessionRequired,
				options: joinOptions(installTargetOptions, productRefOptions, orphansOptions), notes: orphanNotes},
			{name: "remove", summary: "Delete them", id: cmdOrphansRemove,
				session: sessionImplicitLogin,
				options: joinOptions(installTargetOptions, productRefOptions, orphansOptions, []optionID{optYes}), notes: orphanNotes},
		},
	},
	{
		name:    "backup",
		summary: "Manage offline backup files",
		children: []commandNode{
			{
				name:    "list",
				summary: "Show each game's offline backup files",
				id:      cmdBackupList,
				session: sessionRequired,
				options: detailsOptions,
				notes:   listDetailsNotes,
			},
			{
				name:    "download",
				summary: "Download offline backup files (installers, patches, extras, language packs)",
				id:      cmdBackupDownload,
				session: sessionImplicitLogin,
				options: joinOptions([]optionID{optDirectory, optNoSubdirectories, optOutputFile, optInfoThreads},
					subdirOptions,
					[]optionID{
						optInclude, optExclude, optBlacklist,
						optInstallerPlatform, optInstallerLanguage,
						optThreads, optProgressInterval, optCheckFreeSpace,
					}, saveOptions, productRefOptions),
				notes: []string{
					"Downloads offline backup files for the specified game or single file.",
					"-o names the output file when downloading a single file.",
				},
			},
		},
	},
}

func findChild(nodes []commandNode, name string) (commandNode, bool) {
	for _, n := range nodes {
		if n.name == name {
			return n, true
		}
	}
	return commandNode{}, false
}

// accepts reports whether the node (with the shared set) accepts the option.
func (n commandNode) accepts(id optionID) bool {
	if sharedOptions.contains(id) {
		return true
	}
	return containsOption(n.options, id)
}

// joinOptions concatenates option lists into a fresh slice, so a node's list
// never shares its backing array with another node's.
func joinOptions(lists ...[]optionID) []optionID {
	var out []optionID
	for _, list := range lists {
		out = append(out, list...)
	}
	return out
}

func containsOption(ids []optionID, id optionID) bool {
	for _, have := range ids {
		if have == id {
			return true
		}
	}
	return false
}

// optionSet is a small helper for the shared option list.
type optionSet []optionID

func (s optionSet) contains(id optionID) bool { return containsOption(s, id) }

// commandPaths is derived directly from commandTree: commandTree is the single
// source of truth for command topology and paths.
var commandPaths = func() map[commandID]string {
	m := make(map[commandID]string)
	var walk func([]string, []commandNode)
	walk = func(prefix []string, nodes []commandNode) {
		for _, n := range nodes {
			path := append(append([]string{}, prefix...), n.name)
			if n.id != cmdNone {
				m[n.id] = strings.Join(path, " ")
			}
			if len(n.children) > 0 {
				walk(path, n.children)
			}
		}
	}
	walk(nil, commandTree)
	return m
}()

// path is the command's verb path, or "?" for a command that has none.
func (id commandID) path() string {
	if p, ok := commandPaths[id]; ok {
		return p
	}
	return "?"
}
