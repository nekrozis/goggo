package cli

import (
	"bytes"
	"sort"
	"strings"
	"testing"

	"github.com/nekrozis/goggo/internal/config"
	"github.com/nekrozis/goggo/internal/core"
	"github.com/nekrozis/goggo/internal/model"
)

func run(t *testing.T, stdin string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = Run(args, strings.NewReader(stdin), &out, &errOut)
	return code, out.String(), errOut.String()
}

// The topics are generated from the parser's own tables (help.go), so the help
// assertions below read those tables instead of restating what they render: a
// change to the vocabulary is a change to the data, not to the test.

// topicNode resolves a topic path to the node the help renders it from.
func topicNode(t *testing.T, path ...string) commandNode {
	t.Helper()
	node, ok := resolveTopic(path)
	if !ok {
		t.Fatalf("no topic %q in the command tree", strings.Join(path, " "))
	}
	return node
}

// nodeNotes is a node's notes, the sentences its topic prints between the
// summary and the options. A node that declares none has nothing for a topic to
// print, so a caller's loop would pass without checking anything: that is a
// failure, not a pass.
func nodeNotes(t *testing.T, path ...string) []string {
	t.Helper()
	node := topicNode(t, path...)
	if len(node.notes) == 0 {
		t.Fatalf("topic %q declares no notes for its help to print", strings.Join(path, " "))
	}
	return node.notes
}

// commandUsageLine derives the usage line help.go composes for a command:
// ProgramName, the path and the arity placeholder. It is derived here from the
// same three inputs, so a wrong path or arity is still caught while a change to
// the assembler needs no test edit.
func commandUsageLine(t *testing.T, path ...string) string {
	t.Helper()
	node := topicNode(t, path...)
	line := "Usage: " + config.ProgramName + " " + strings.Join(path, " ")
	switch want, count := commandArity(node.id); {
	case count == -2:
		line += " [" + want + "]..."
	case count < 0:
		line += " <" + want + ">..."
	case count > 0:
		line += " <" + want + ">"
	}
	return line
}

// namespaceUsageLine derives the usage line of a pure namespace topic.
func namespaceUsageLine(path ...string) string {
	return "Usage: " + config.ProgramName + " " + strings.Join(path, " ") + " <subcommand> [options]"
}

// optionLong is one option's long name as the help spells it.
func optionLong(id optionID) string { return "--" + optionName(id) }

// optionLongs lists the long names of a set of options in table order — the
// order optionLines renders them in.
func optionLongs(ids optionSet) []string {
	var longs []string
	for i := range optionTable {
		spec := &optionTable[i]
		if spec.hidden || !ids.contains(spec.id) {
			continue
		}
		longs = append(longs, optionLong(spec.id))
	}
	return longs
}

// nodeOptionLongs lists every option a command's topic prints: the shared set
// plus the node's own, in table order.
func nodeOptionLongs(node commandNode) []string {
	var longs []string
	for i := range optionTable {
		spec := &optionTable[i]
		if spec.hidden || !node.accepts(spec.id) {
			continue
		}
		longs = append(longs, optionLong(spec.id))
	}
	return longs
}

// topicRow reports whether the topic lists one subcommand: it looks for a line
// that begins with the subcommand's name and carries its summary. Reading the
// line that way rather than searching the whole topic for the name is what makes
// the check mean "the topic lists this subcommand" — a name as short as "file"
// also occurs in the surrounding sentences.
func topicRow(topic string, child commandNode) bool {
	for _, line := range strings.Split(topic, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, child.name+" ") && strings.Contains(trimmed, child.summary) {
			return true
		}
	}
	return false
}

// optionSpecByID finds the option table entry an id names.
func optionSpecByID(t *testing.T, id optionID) *optionSpec {
	t.Helper()
	for i := range optionTable {
		if optionTable[i].id == id {
			return &optionTable[i]
		}
	}
	t.Fatalf("option %d is not in the option table", id)
	return nil
}

// optionHelpLines is an option's help as a topic prints it: the summary line
// plus the lines of its longer detail. An option that declares no help has
// nothing for a topic to print, so a caller's loop would pass without checking
// anything: that is a failure, not a pass.
func optionHelpLines(t *testing.T, id optionID) []string {
	t.Helper()
	spec := optionSpecByID(t, id)
	if spec.summary == "" || spec.detail == "" {
		t.Fatalf("--%s declares no help for its topic to print", spec.long)
	}
	lines := []string{spec.summary}
	for _, line := range strings.Split(spec.detail, "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// TestRunHelpAndVersion covers the two meta answers, which run before any
// session work. The help is generated from the parser's tables, so it
// lists the command surface rather than a hand-kept option list.
func TestRunHelpAndVersion(t *testing.T) {
	// The root topic opens with the usage line, which names the program.
	usagePrefix := "Usage: " + config.ProgramName + " "
	for _, flag := range []string{"--help", "-h"} {
		code, out, errOut := run(t, "", flag)
		if code != 0 {
			t.Errorf("%s exit = %d, want 0", flag, code)
		}
		if !strings.HasPrefix(out, usagePrefix) {
			t.Errorf("%s output must start with the usage line, got %q", flag, out)
		}
		if !strings.Contains(out, "Commands:") {
			t.Errorf("%s output is missing the command surface: %q", flag, out)
		}
		if errOut != "" {
			t.Errorf("%s stderr = %q", flag, errOut)
		}
	}

	// --version carries the full identity: our version plus the compatibility
	// baseline it tracks. These two lines are a contract pin — a script reads
	// them — so the config layer's identity constants are pinned here through
	// the output the CLI actually prints.
	for _, arg := range []string{"--version", "version"} {
		code, out, _ := run(t, "", arg)
		if code != 0 {
			t.Errorf("%s exit = %d, want 0", arg, code)
		}
		lines := strings.Split(strings.TrimSpace(out), "\n")
		wantVersion := config.VersionString
		wantCompat := config.UpstreamName + " compatibility: " + config.UpstreamCompatibilityVersion
		if len(lines) != 2 || lines[0] != wantVersion || lines[1] != wantCompat {
			t.Errorf("%s output = %q, want %q + %q", arg, out, wantVersion, wantCompat)
		}
	}

	// A bare invocation is a usage failure: it shows the surface and does not
	// look like a successful run.
	code, out, errOut := run(t, "")
	if code != 2 || out != "" || !strings.Contains(errOut, usagePrefix) {
		t.Errorf("bare invocation = %d/%q/%q, want a usage failure on stderr", code, out, errOut)
	}
}

// TestRunFailures locks the failure contract: anything the
// parser or the command tree refuses is a usage failure and exits 2, with the
// diagnostic on stderr and nothing on stdout. Removed commands are unknown
// commands now, not "not implemented" options — their own case is
// TestRunHelpForACommandAndRemovedCommands.
//
// secret is the value a case hands to an option, when that value is something
// that must not come back out: the refusal may name the option, never what was
// offered as its value.
func TestRunFailures(t *testing.T) {
	cases := []struct {
		name   string
		args   []string
		want   string
		secret string
	}{
		{"unknown option", []string{"--nonsense"}, "unknown option", ""},
		{"unknown command", []string{"frobnicate"}, "unknown command", ""},
		{"removed option", []string{"--download"}, "unknown option", ""},
		{"removed list option", []string{"--list", "details"}, "unknown option", ""},
		{"missing value", []string{"list", "games", "--tag"}, "requires a value", ""},
		{"invalid platform", []string{"install", "123", "--platform", "nope"}, "invalid value for --platform", ""},
		{"unaccepted option", []string{"list", "games", "--threads", "8"}, "not accepted", ""},
		{"missing game", []string{"install"}, "needs a game", ""},
		{"malformed target", []string{"install", "/2"}, "the game is empty", ""},
		{"show builds with a build", []string{"show", "builds", "123/2"}, "not a build", ""},
		{"bare invocation", nil, "Usage:", ""},
		{"password argument", []string{"auth", "login", "--password", "hunter2"}, "unknown option", "hunter2"},
		{"password stdin", []string{"auth", "login", "--password-stdin"}, "unknown option", ""},
		{"token stdin", []string{"auth", "login", "--token-stdin"}, "unknown option", ""},
		{"non-interactive", []string{"auth", "login", "--non-interactive"}, "unknown option", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, out, errOut := run(t, "", c.args...)
			if code != 2 {
				t.Errorf("exit = %d, want 2 (usage failure)", code)
			}
			if out != "" {
				t.Errorf("stdout = %q, want empty", out)
			}
			if !strings.Contains(errOut, c.want) {
				t.Errorf("stderr = %q, want it to contain %q", errOut, c.want)
			}
			if c.secret != "" {
				if strings.Contains(out, c.secret) || strings.Contains(errOut, c.secret) {
					t.Errorf("the refusal echoed the supplied value: stdout %q / stderr %q", out, errOut)
				}
			}
		})
	}
}

// stripANSI drops the SGR sequences a coloured line is wrapped in, so a layout
// assertion does not also pin the colour scheme.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\033' {
			b.WriteByte(s[i])
			continue
		}
		for i < len(s) && s[i] != 'm' {
			i++
		}
	}
	return b.String()
}

// TestRenderGames locks the listing's layout and its one piece of state
// marking: one line per game, the update count in brackets, one "+> " line per
// DLC, and colour on the rows that carry state. The layout is compared with the
// colour stripped, so changing the palette is not a change to the list.
func TestRenderGames(t *testing.T) {
	items := []model.GameItem{
		{Name: "with-updates-new", Updates: 3, IsNew: true, DLCNames: []string{"dlc one", "dlc two"}},
		{Name: "with-updates-old", Updates: 1, IsNew: false},
		{Name: "new-only", IsNew: true},
		{Name: "plain"},
	}

	var buf bytes.Buffer
	if err := renderGames(&buf, items, true); err != nil {
		t.Fatalf("renderGames: %v", err)
	}
	coloured := buf.String()
	want := strings.Join([]string{
		"with-updates-new [3]",
		"+> dlc one",
		"+> dlc two",
		"with-updates-old [1]",
		"new-only",
		"plain",
		"",
	}, "\n")
	if got := stripANSI(coloured); got != want {
		t.Errorf("coloured layout:\n got %q\nwant %q", got, want)
	}

	// Colour marks a new game and a game with updates — and nothing else.
	marked := map[string]bool{
		"with-updates-new [3]": true,
		"with-updates-old [1]": true,
		"new-only":             true,
	}
	for _, line := range strings.Split(strings.TrimSuffix(coloured, "\n"), "\n") {
		if got, want := strings.Contains(line, "\033["), marked[stripANSI(line)]; got != want {
			t.Errorf("line %q coloured = %v, want %v: colour marks a new game or one with updates", line, got, want)
		}
	}

	buf.Reset()
	if err := renderGames(&buf, items, false); err != nil {
		t.Fatalf("renderGames: %v", err)
	}
	if strings.Contains(buf.String(), "\033[") {
		t.Errorf("plain output must not contain escapes: %q", buf.String())
	}
	if !strings.Contains(buf.String(), "with-updates-new [3]\n") {
		t.Errorf("plain output = %q", buf.String())
	}
}

func TestRenderTags(t *testing.T) {
	var buf bytes.Buffer
	if err := renderTags(&buf, map[string]string{"zeta": "Z", "alpha": "A"}); err != nil {
		t.Fatalf("renderTags: %v", err)
	}
	if got, want := buf.String(), "alpha = A\nzeta = Z\n"; got != want {
		t.Errorf("tags = %q, want %q", got, want)
	}

	buf.Reset()
	if err := renderTags(&buf, nil); err != nil {
		t.Fatalf("renderTags: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("empty tags = %q", buf.String())
	}
}

// TestRenderWishlist locks the wishlist layout, including the UTC release date.
func TestRenderWishlist(t *testing.T) {
	items := []model.WishlistItem{
		{
			Tags:                       []string{"Coming soon", "Discount"},
			Title:                      "Wanted",
			Currency:                   "$",
			Price:                      "12.500000$",
			DiscountPercent:            "50%",
			Discount:                   "1.25$",
			StoreCredit:                "0.500000$",
			URL:                        "https://www.gog.com/game/wanted",
			ReleaseDateTime:            1700000000, // 2023-Nov-14 22:13:20 UTC
			Platform:                   config.PlatformWindows | config.PlatformLinux,
			IsDiscounted:               true,
			IsBonusStoreCreditIncluded: true,
		},
		{Title: "Plain", Price: "0.000000$", URL: "https://www.gog.com/game/plain"},
	}

	var buf bytes.Buffer
	if err := renderWishlist(&buf, items); err != nil {
		t.Fatalf("renderWishlist: %v", err)
	}
	want := strings.Join([]string{
		"Wanted [Coming soon, Discount]",
		"\thttps://www.gog.com/game/wanted",
		"\tPlatforms: Windows, Linux",
		"\tRelease date: 2023-Nov-14 22:13:20",
		"\tPrice: 12.500000$ (-50% | -1.25$)",
		"\tStore credit: 0.500000$",
		"",
		"Plain",
		"\thttps://www.gog.com/game/plain",
		"\tPrice: 0.000000$",
		"",
		"",
	}, "\n")
	if buf.String() != want {
		t.Errorf("wishlist output:\n got %q\nwant %q", buf.String(), want)
	}
}

// The old argument helper (galaxyCommandArgument) is gone: splitting
// "<game>[/<build>]" now happens in the parser, and its shape rules are locked
// by TestParseTargetErrors and TestParseShowCommands in options_test.go.

// TestRunMalformedTargetFailsOffline locks that a malformed target fails before
// any session work: the parser answers, so no directory is created and no
// request is made.
func TestRunMalformedTargetFailsOffline(t *testing.T) {
	for _, args := range [][]string{
		{"show", "builds", "/"},
		{"install", "/"},
		{"show", "cdns", "1/2/3"},
	} {
		code, out, errOut := run(t, "", args...)
		if code != 2 {
			t.Errorf("run(%v) exit = %d, want 2", args, code)
		}
		if out != "" {
			t.Errorf("run(%v) stdout = %q, want empty", args, out)
		}
		if !strings.Contains(errOut, "invalid target") {
			t.Errorf("run(%v) stderr = %q, want the argument error", args, errOut)
		}
	}
}

// TestRunHelpListsTheCommandSurface keeps the help in step with the parser:
// every command and every shared option the CLI accepts is listed, and nothing
// it removed is. The names come from the tables the help is generated from, so
// the surface is compared with itself rather than with a second copy.
func TestRunHelpListsTheCommandSurface(t *testing.T) {
	_, out, _ := run(t, "", "--help")

	// Every command and subcommand of the tree, meta commands included, must be
	// reachable from the root topic.
	for _, node := range append(append([]commandNode{}, commandTree...), metaCommands...) {
		names := []string{node.name}
		for _, child := range node.children {
			names = append(names, child.name)
		}
		for _, name := range names {
			if !strings.Contains(out, name) {
				t.Errorf("--help output does not mention the %s command: %q", name, out)
			}
		}
	}

	// The root topic shows the shared options; a command's own options
	// (--threads, --directory,...) belong to its topic.
	for _, long := range optionLongs(sharedOptions) {
		if !strings.Contains(out, long) {
			t.Errorf("--help output does not mention the shared %s: %q", long, out)
		}
	}

	// Nothing the parser deliberately dropped is advertised again. It could come
	// back two ways: as text in the topic, or as a shared option carrying its old
	// name — the second is invisible to the first, because a live option's name
	// is exactly what the topic is supposed to print.
	live := make(map[string]bool, len(optionTable))
	for i := range optionTable {
		live[optionTable[i].long] = true
	}
	var removed []string
	for name := range removedOptions {
		if !live[name] {
			removed = append(removed, name)
		}
	}
	sort.Strings(removed)
	for _, name := range removed {
		if strings.Contains(out, "--"+name) {
			t.Errorf("--help still advertises the removed --%s: %q", name, out)
		}
	}
	for i := range optionTable {
		if spec := &optionTable[i]; sharedOptions.contains(spec.id) {
			if _, gone := removedOptions[spec.long]; gone {
				t.Errorf("--help advertises the removed --%s as a shared option: %q", spec.long, out)
			}
		}
	}
}

// TestRunHelpForACommandAndRemovedCommands locks the two meta answers that do
// not need a session: a command topic renders from the tree, and a removed
// command is unknown (with the migration hint when one exists).
func TestRunHelpForACommandAndRemovedCommands(t *testing.T) {
	if code, out, _ := run(t, "", "help", "install"); code != 0 || !strings.Contains(out, commandUsageLine(t, "install")) {
		t.Errorf("help install = %d/%q, want the topic", code, out)
	}
	if code, out, _ := run(t, "", "install", "-h"); code != 0 || !strings.Contains(out, optionLong(optPlatform)) {
		t.Errorf("install -h = %d/%q, want the command's options", code, out)
	}
	if code, _, errOut := run(t, "", "repair"); code != 2 || !strings.Contains(errOut, "unknown command") {
		t.Errorf("removed command = %d/%q, want a usage failure", code, errOut)
	}
	if code, _, errOut := run(t, "", "--galaxy-install", "123"); code != 2 || !strings.Contains(errOut, "hint: use") {
		t.Errorf("removed option = %d/%q, want the hint", code, errOut)
	}
}

// The install argument is parsed by the same target rules as the show commands,
// so its malformed cases are covered by TestRunMalformedTargetFailsOffline.

// TestRenderBuilds locks the listing line.
//
// Contract (format): the builds listing is one line per build with fixed labels
// — index, version, date, generation, build id — and a user reads or copies that
// line as it is, so the bytes of the line are the contract, not a set of tokens
// that happen to appear in it.
func TestRenderBuilds(t *testing.T) {
	var buf bytes.Buffer
	rows := []core.BuildRow{
		{Index: 0, VersionName: "1.0.2", DatePublished: "2024-03-02", Generation: 2, BuildID: "b-new"},
		{Index: 1, VersionName: "1.0.1", DatePublished: "2024-01-05", Generation: 1, BuildID: "b-old"},
	}
	if err := renderBuilds(&buf, rows); err != nil {
		t.Fatalf("renderBuilds: %v", err)
	}
	want := "0: Version 1.0.2 - 2024-03-02 (Gen 2) (Build id: b-new)\n" +
		"1: Version 1.0.1 - 2024-01-05 (Gen 1) (Build id: b-old)\n"
	if buf.String() != want {
		t.Errorf("got %q\nwant %q", buf.String(), want)
	}

	buf.Reset()
	if err := renderBuilds(&buf, nil); err != nil {
		t.Fatalf("renderBuilds(nil): %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("empty listing = %q, want nothing", buf.String())
	}
}

// TestRenderManifest locks the three properties of the styled output: tab
// indentation, keys in byte order and no HTML escaping. A user pasting the
// document into a tool must find the URLs intact, so the & of a query string may
// not become \u0026.
func TestRenderManifest(t *testing.T) {
	var buf bytes.Buffer
	doc := map[string]any{
		"z": "https://cdn.gog.com/x?a=1&b=<2>",
		"a": map[string]any{"items": []any{float64(2), true}},
	}
	if err := renderManifest(&buf, docOf(doc)); err != nil {
		t.Fatalf("renderManifest: %v", err)
	}
	want := "{\n" +
		"\t\"a\": {\n" +
		"\t\t\"items\": [\n" +
		"\t\t\t2,\n" +
		"\t\t\ttrue\n" +
		"\t\t]\n" +
		"\t},\n" +
		"\t\"z\": \"https://cdn.gog.com/x?a=1&b=<2>\"\n" +
		"}\n"
	if buf.String() != want {
		t.Errorf("got %q\nwant %q", buf.String(), want)
	}
}

// TestRenderCDNNames locks the one-name-per-line output.
func TestRenderCDNNames(t *testing.T) {
	var buf bytes.Buffer
	if err := renderCDNNames(&buf, []string{"gog-cdn-fastly", "gog-cdn-cloudflare"}); err != nil {
		t.Fatalf("renderCDNNames: %v", err)
	}
	if want := "gog-cdn-fastly\ngog-cdn-cloudflare\n"; buf.String() != want {
		t.Errorf("got %q, want %q", buf.String(), want)
	}
}

// TestRenderNotice locks the stream split: support and generation messages go to
// stdout, argument-resolution failures to stderr.
func TestRenderNotice(t *testing.T) {
	var out, errOut bytes.Buffer

	renderNotice(&out, &errOut, core.Notice{})
	if out.Len() != 0 || errOut.Len() != 0 {
		t.Errorf("an empty notice wrote %q / %q, want nothing", out.String(), errOut.String())
	}

	renderNotice(&out, &errOut, core.Notice{Text: "Only generation 2 builds are supported currently"})
	if want := "Only generation 2 builds are supported currently\n"; out.String() != want {
		t.Errorf("stdout = %q, want %q", out.String(), want)
	}
	if errOut.Len() != 0 {
		t.Errorf("stderr = %q, want nothing for a stdout notice", errOut.String())
	}

	out.Reset()
	renderNotice(&out, &errOut, core.Notice{Text: "Didn't match any products", Err: true})
	if out.Len() != 0 {
		t.Errorf("stdout = %q, want nothing for a stderr notice", out.String())
	}
	if want := "Didn't match any products\n"; errOut.String() != want {
		t.Errorf("stderr = %q, want %q", errOut.String(), want)
	}
}

// TestRunHelpTopicsLockTheirContent locks the layered help: the
// root topic lists the surface and says how to reach a command's own options, a
// command topic lists exactly what that command accepts (with the sentences a
// user needs), and the orphan topics carry the cross-platform warning before a
// destructive run.
//
// Everything a topic states about a command is read from that command's own
// node and options, so a topic that stops rendering one of them fails here
// without the test keeping a second copy of the vocabulary.
func TestRunHelpTopicsLockTheirContent(t *testing.T) {
	_, root, _ := run(t, "", "--help")
	if !strings.Contains(root, "Run 'goggo <command> -h'") {
		t.Errorf("root help does not point at the per-command topics: %q", root)
	}
	if strings.Contains(root, optionLong(optThreads)) {
		t.Errorf("root help lists a command-only option: %q", root)
	}

	install := topicNode(t, "install")
	_, installTopic, _ := run(t, "", "install", "-h")
	if want := commandUsageLine(t, "install"); !strings.Contains(installTopic, want) {
		t.Errorf("install topic is missing its usage line %q: %q", want, installTopic)
	}
	for _, long := range nodeOptionLongs(install) {
		if !strings.Contains(installTopic, long) {
			t.Errorf("install topic is missing %s: %q", long, installTopic)
		}
	}
	// The install topic names the templates --install-dir accepts, because a
	// whitelist the user cannot read is a whitelist the user cannot use: the
	// list is the resolver's, and the sentence stating that a template is
	// matched whole is the option's own help line.
	for _, template := range core.InstallSubdirTemplates {
		if !strings.Contains(installTopic, template) {
			t.Errorf("install topic must name the %s template: %q", template, installTopic)
		}
	}
	for _, line := range optionHelpLines(t, optInstallDir) {
		if !strings.Contains(installTopic, line) {
			t.Errorf("install topic must carry the --install-dir help line %q: %q", line, installTopic)
		}
	}

	auth := topicNode(t, "auth")
	_, authTopic, _ := run(t, "", "help", "auth")
	if want := namespaceUsageLine("auth"); !strings.Contains(authTopic, want) {
		t.Errorf("auth topic is missing its usage line %q: %q", want, authTopic)
	}
	for _, child := range auth.children {
		if !topicRow(authTopic, child) {
			t.Errorf("auth topic is missing the %s subcommand (%q): %q", child.name, child.summary, authTopic)
		}
	}

	// The cross-platform warning a destructive run must state comes from the
	// node's own notes.
	for _, path := range [][]string{{"orphans", "check"}, {"orphans", "remove"}} {
		_, out, _ := run(t, "", append(append([]string{}, path...), "-h")...)
		for _, note := range nodeNotes(t, path...) {
			if !strings.Contains(out, note) {
				t.Errorf("%v does not carry the node's warning %q: %q", path, note, out)
			}
		}
	}

	// The topic rule reaches the exit code: a known topic is success, an
	// unknown one is a usage failure.
	if code, _, _ := run(t, "", "help", "install"); code != 0 {
		t.Errorf("help install exit = %d, want 0", code)
	}
	if code, _, errOut := run(t, "", "help", "bogus"); code != 2 || !strings.Contains(errOut, "unknown command") {
		t.Errorf("help bogus = %d/%q, want a usage failure", code, errOut)
	}
	if code, _, _ := run(t, "", "install", "-h"); code != 0 {
		t.Errorf("install -h exit = %d, want 0", code)
	}
}
