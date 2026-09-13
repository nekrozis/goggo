package cli

import "github.com/nekrozis/goggo/internal/config"

// The command tree the CLI is built on (CLI1, review D1/D13/D14).
//
// This file is the parser's data and vocabulary: which commands exist, how they
// nest, what each node accepts, and what a resolved command carries. It holds no
// behaviour — no core calls, no rendering — so the tree can be read as the
// product surface it is: only commands this build actually supports appear here
// (D14), namespaces exist only where several natural actions share a stable
// domain (D13: auth, orphans), and every other upstream verb is absent rather
// than present-and-unimplemented.

// sessionClass is what a command needs from the session before it can run, and
// whether it may create one (review CLI1 §7).
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
	// `auth status` answers "Not logged in" itself and `auth logout` only
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
// Namespaces (auth, list, show, orphans) have no id; they only group their
// children.
type commandID uint8

const (
	cmdNone commandID = iota

	cmdAuthLogin
	cmdAuthLogout
	cmdAuthStatus

	cmdListGames
	cmdListTags
	cmdListWishlist

	cmdShowBuilds
	cmdShowManifest
	cmdShowCDNs

	cmdInstall
	cmdVerify

	cmdOrphansCheck
	cmdOrphansRemove
)

// metaAction is what a meta invocation asks for. Meta commands are built in:
// they short-circuit before any business dispatch (D18).
type metaAction uint8

const (
	metaNone metaAction = iota
	metaHelp
	metaVersion
)

// target is the "<product id or gamename>[/<build id or index>]" argument the
// Galaxy-backed commands take. Splitting it is the parser's job: the dispatcher
// receives the two parts, never the joined string (review D17 — the CLI's
// vocabulary is its own).
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
	// policy the dispatcher applies are the same value (review S4).
	session sessionClass
	target  target
	// yes carries the destructive-confirmation flag. Only the destructive
	// commands accept it (D16).
	yes bool
}

// commandNode is one node of the tree.
//
// options lists what the node ADDS to the shared set (sharedOptions): the parser
// checks an option against common ∪ node, so "global" means shared semantics,
// not "every command accepts it" (D15). id is cmdNone for namespaces.
//
// session is what the command needs from the session before it can run. Every
// leaf declares one; namespaces and the meta commands do not, because nothing
// dispatches them.
type commandNode struct {
	name     string
	summary  string
	id       commandID
	session  sessionClass
	options  []optionID
	children []commandNode
	// notes are the lines a reader must see before running the command: the
	// help prints them between the summary and the options (review §5,
	// constraint B).
	notes []string
}

// orphanNotes is the warning both orphan commands carry.
//
// The single-manifest ledger is this port's model — upstream forces every
// platform and language when it checks orphans (downloader.cpp:1678-1685) — so
// files belonging to another variant can be reported as orphaned. A destructive
// command must say that before it runs, not only in an audit file.
var orphanNotes = []string{
	"Orphan detection uses the manifest of the selected platform and language:",
	"files belonging to other variants may be reported as orphaned.",
}

// The installation-locating options, shared by every command that has to
// resolve an install path.
//
// installPath is derived from all of these at once
// (core/plan.go: directory + the resolved subdirectory template under the
// selected platform/language/arch), so a command that accepts only some of them
// would resolve a different root than its siblings and report on the wrong tree
// (review §13⑤).
var installTargetOptions = []optionID{
	optDirectory,
	optInstallDir,
	optNoSubdirectories,
	optPlatform,
	optLanguage,
	optArch,
}

// listGamesOptions filters the games listing (D7: filters belong to the command,
// not to the CLI at large).
//
// The installer filters are the listing side of platform/language. They are
// accepted by `list games` — the listing that shows installers — and not by the
// other list resources, where they would filter nothing; that is the same
// "shared semantics, per-command acceptance" rule as everywhere else (D15).
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

// verifyOptions are what a read-only verification honours. Upstream's
// --status filters by the include mask and honours the blacklist, so verify
// follows it; the orphan walk does not (D2 in the CLI1 plan, §13①).
var verifyOptions = []optionID{
	optInclude,
	optExclude,
	optBlacklist,
}

// orphansOptions are the filter files the walk consults. include/exclude are
// deliberately absent: upstream checks orphans over everything ("Always check
// everything when checking for orphaned files", downloader.cpp:1678-1685).
var orphansOptions = []optionID{
	optIgnorelist,
	optBlacklist,
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
			{name: "logout", summary: "Log out (clear local login state)", id: cmdAuthLogout,
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
		name:    "show",
		summary: "Show one product's builds or endpoints",
		children: []commandNode{
			{name: "builds", summary: "List a product's builds", id: cmdShowBuilds,
				session: sessionRequired, options: []optionID{optSort}},
			{name: "manifest", summary: "Show a build's manifest", id: cmdShowManifest,
				session: sessionRequired},
			{name: "cdns", summary: "List a build's CDN endpoints", id: cmdShowCDNs,
				session: sessionRequired},
		},
	},
	{
		name:    "install",
		summary: "Make the local installation match the manifest",
		id:      cmdInstall,
		session: sessionImplicitLogin,
		options: joinOptions(installTargetOptions, []optionID{
			optThreads,
			optProgressInterval,
			optCDNPriority,
			optNoDependencies,
			optCheckFreeSpace,
		}),
	},
	{
		name:    "verify",
		summary: "Report whether the local files match the manifest",
		id:      cmdVerify,
		session: sessionRequired,
		options: joinOptions(installTargetOptions, verifyOptions),
		// The report's vocabulary is the upstream status codes, so the topic has
		// to define them; and a verification never repairs, which a reader has
		// to know before relying on it (review S5).
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
				options: joinOptions(installTargetOptions, orphansOptions), notes: orphanNotes},
			{name: "remove", summary: "Delete them", id: cmdOrphansRemove,
				session: sessionImplicitLogin,
				options: joinOptions(installTargetOptions, orphansOptions, []optionID{optYes}), notes: orphanNotes},
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

// commandPaths maps every command to the verb path a user types. Diagnostics use
// it so a failure names the command the way the help does.
var commandPaths = map[commandID]string{
	cmdAuthLogin:     "auth login",
	cmdAuthLogout:    "auth logout",
	cmdAuthStatus:    "auth status",
	cmdListGames:     "list games",
	cmdListTags:      "list tags",
	cmdListWishlist:  "list wishlist",
	cmdShowBuilds:    "show builds",
	cmdShowManifest:  "show manifest",
	cmdShowCDNs:      "show cdns",
	cmdInstall:       "install",
	cmdVerify:        "verify",
	cmdOrphansCheck:  "orphans check",
	cmdOrphansRemove: "orphans remove",
}

// path is the command's verb path, or "?" for a command that has none.
func (id commandID) path() string {
	if p, ok := commandPaths[id]; ok {
		return p
	}
	return "?"
}
