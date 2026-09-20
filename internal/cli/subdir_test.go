package cli

import (
	"strings"
	"testing"

	"github.com/nekrozis/goggo/internal/config"
)

// The six --subdir-* options, their defaults and the per-field whole-template
// whitelist. Each domain is its own language — the install-dir table must not
// leak into these, and these must not leak into it.

// subdirField reads the DirectoryConfig field one domain writes.
func subdirField(conf config.DirectoryConfig, name string) string {
	switch name {
	case "installers":
		return conf.InstallersSubdir
	case "extras":
		return conf.ExtrasSubdir
	case "patches":
		return conf.PatchesSubdir
	case "language-packs":
		return conf.LanguagePackSubdir
	case "dlc":
		return conf.DLCSubdir
	case "game":
		return conf.GameSubdir
	}
	return ""
}

// TestParseInstallerGateDefaults locks the conversion's installer
// platform/language defaults ("w+l" and "en"), parsed into both the mask and the
// priority list — without them every non-extras vector silently drops from a
// download run.
func TestParseInstallerGateDefaults(t *testing.T) {
	inv := mustParse(t, "download", "g")
	if inv.cfg.DownloadConfig.InstallerPlatform != config.PlatformWindows|config.PlatformLinux {
		t.Errorf("installer platform = %d, want windows+linux", inv.cfg.DownloadConfig.InstallerPlatform)
	}
	if inv.cfg.DownloadConfig.InstallerLanguage != config.LangEN {
		t.Errorf("installer language = %d, want en", inv.cfg.DownloadConfig.InstallerLanguage)
	}
	// "w+l" is ONE comma-group: the priority list carries one entry with both
	// bits.
	if want := config.PlatformWindows | config.PlatformLinux; len(inv.cfg.DownloadConfig.PlatformPriority) != 1 ||
		inv.cfg.DownloadConfig.PlatformPriority[0] != want {
		t.Errorf("platform priority = %v, want [windows+linux]", inv.cfg.DownloadConfig.PlatformPriority)
	}
	if len(inv.cfg.DownloadConfig.LanguagePriority) != 1 || inv.cfg.DownloadConfig.LanguagePriority[0] != config.LangEN {
		t.Errorf("language priority = %v, want [en]", inv.cfg.DownloadConfig.LanguagePriority)
	}

	// An explicit option overrides the default through the same closure.
	inv = mustParse(t, "download", "g", "--installer-platform", "mac")
	if inv.cfg.DownloadConfig.InstallerPlatform != config.PlatformMac {
		t.Errorf("--installer-platform mac = %d, want mac only", inv.cfg.DownloadConfig.InstallerPlatform)
	}
}

// TestParseRemoteXMLDefault locks the other default: remote XML is on — without
// it the installer/patch version check silently never runs.
func TestParseRemoteXMLDefault(t *testing.T) {
	inv := mustParse(t, "download", "g")
	if !inv.cfg.DownloadConfig.RemoteXML {
		t.Error("RemoteXML = false, want the default true")
	}
}

// TestSubdirDefaults locks the values applyParseDefaults writes for the six
// domains: the parsed default is what the download path actually uses, so this
// is the CLI's half of the config table's defaults. The expected values are the
// table's own, so a change of default is a change to config, not to the test.
func TestSubdirDefaults(t *testing.T) {
	inv := mustParse(t, "download", "some_game")
	for _, opt := range config.SubdirOptions {
		if got := subdirField(inv.cfg.Directories, opt.Name); got != opt.Default {
			t.Errorf("parsed default --subdir-%s = %q, want the config table's %q", opt.Name, got, opt.Default)
		}
	}
}

// TestSubdirWhitelistIsPerField locks the independent domains: each option
// accepts any literal and only its own whole templates; a template belonging
// to another domain, an embedded placeholder or a half-spelled template is a
// usage error.
func TestSubdirWhitelistIsPerField(t *testing.T) {
	accepted := map[string][]string{
		"installers":     {"", "setup", "%platform%", "%version%"},
		"extras":         {"extras", "bonus", "%platform%", "%version%"},
		"patches":        {"patches", "%platform%", "%version%"},
		"language-packs": {"languagepacks", "langs", "%platform%", "%version%"},
		"dlc":            {"dlc", "dlc/%dlcname%", "%dlcname%", "%dlc_title%", "%dlc_title_stripped%"},
		"game":           {"games", "%gamename%", "%gamename_firstletter%", "%title%", "%title_stripped%"},
	}
	for name, values := range accepted {
		for _, v := range values {
			inv := mustParse(t, "download", "g", "--subdir-"+name, v)
			if got := subdirField(inv.cfg.Directories, name); got != v {
				t.Errorf("--subdir-%s %q stored %q, want it kept whole", name, v, got)
			}
		}
	}

	refused := map[string][]string{
		"installers":     {"%gamename%", "%dlcname%", "%install_dir%", "%platform%/%version%", "a%platform%"},
		"extras":         {"%gamename%", "%dlcname%", "extras/%platform%", "%version%x"},
		"patches":        {"%gamename%", "%dlcname%", "%platform%/x"},
		"language-packs": {"%gamename%", "%title%", "%lang%"},
		"dlc":            {"%gamename%", "%platform%", "dlc/%gamename%", "%dlcname%/", "x/%dlcname%"},
		"game":           {"%dlcname%", "%platform%", "%version%", "%gamename%/data", "x%title%"},
	}
	for name, values := range refused {
		for _, v := range values {
			err := mustUsageError(t, "download", "g", "--subdir-"+name, v)
			if !strings.Contains(err.Error(), "known templates") {
				t.Errorf("--subdir-%s %q: error = %v, want the whitelist refusal", name, v, err)
			}
		}
	}
}

// TestSubdirOptionsAreDownloadOnly locks the domain: the six options belong to
// the download commands; install keeps its own directory vocabulary.
func TestSubdirOptionsAreDownloadOnly(t *testing.T) {
	for _, args := range [][]string{
		{"install", "123", "--subdir-extras", "x"},
		{"verify", "123", "--subdir-game", "%gamename%"},
		{"list", "games", "--subdir-dlc", "d"},
	} {
		err := mustUsageError(t, args...)
		if !strings.Contains(err.Error(), "not accepted") {
			t.Errorf("parseArgs(%v) error = %v, want the per-command refusal", args, err)
		}
	}
	// Both download commands accept all six.
	for _, base := range [][]string{{"download", "g"}, {"download", "file", "g/1"}} {
		for _, name := range []string{"installers", "extras", "patches", "language-packs", "dlc", "game"} {
			if _, err := parseArgs(append(append([]string{}, base...), "--subdir-"+name, "x"), config.Config{}); err != nil {
				t.Errorf("%v --subdir-%s: %v", base, name, err)
			}
		}
	}
}

// TestDownloadDualStateResolution locks the dual-state resolution:
// "download <game>..." is the leaf, "download file <spec>..." the subcommand,
// and the word "file" can never name a batch game.
func TestDownloadDualStateResolution(t *testing.T) {
	inv := mustParse(t, "download", "terraria")
	if inv.cmd != cmdDownload || len(inv.args) != 1 || inv.args[0] != "terraria" {
		t.Errorf("download terraria = cmd %v args %v", inv.cmd, inv.args)
	}
	inv = mustParse(t, "download", "a", "b", "c")
	if inv.cmd != cmdDownload || len(inv.args) != 3 {
		t.Errorf("variadic games = %v", inv.args)
	}
	inv = mustParse(t, "download", "file", "terraria/123", "a/b/c")
	if inv.cmd != cmdDownloadFile || len(inv.args) != 2 {
		t.Errorf("download file = cmd %v args %v", inv.cmd, inv.args)
	}
	// The batch leaf keeps the no-argument refusal: no implicit account-wide
	// download exists.
	err := mustUsageError(t, "download")
	if !strings.Contains(err.Error(), "needs a game") {
		t.Errorf("bare download = %v, want the needs-a-game refusal", err)
	}
	// A slashed game name is the subcommand's shape, and the hint says so.
	err = mustUsageError(t, "download", "terraria/123")
	if !strings.Contains(err.Error(), "download file") {
		t.Errorf("slashed game = %v, want the download file hint", err)
	}
}

// TestOutputFileRules locks -o: only download file accepts it, it pairs with
// exactly one spec at the parser layer, and the value is kept raw for the
// dispatcher (which refuses directories).
func TestOutputFileRules(t *testing.T) {
	inv := mustParse(t, "download", "file", "g/1", "-o", "out.zip")
	if inv.outputFile != "out.zip" {
		t.Errorf("-o = %q, want it stored", inv.outputFile)
	}
	err := mustUsageError(t, "download", "file", "g/1", "g/2", "-o", "out.zip")
	if !strings.Contains(err.Error(), "exactly one spec") {
		t.Errorf("-o with two specs = %v, want the parser refusal", err)
	}
	err = mustUsageError(t, "download", "g", "-o", "out.zip")
	if !strings.Contains(err.Error(), "not accepted") {
		t.Errorf("-o on the batch leaf = %v, want the per-command refusal", err)
	}
}

// TestDownloadTopicsNameTheirTemplates applies the rule that a whitelist the
// user cannot read is a whitelist the user cannot use to the six subdir domains:
// each option's help must list exactly its own templates. The usage lines, the
// subcommand name and the sentences the topic carries are derived from the
// parser's own data — the tree, the arity table and the option table — so the
// test states what the topic must say, not how it spells it.
func TestDownloadTopicsNameTheirTemplates(t *testing.T) {
	download := topicNode(t, "download")
	_, topic, _ := run(t, "", "download", "-h")

	if want := commandUsageLine(t, "download"); !strings.Contains(topic, want) {
		t.Errorf("download topic is missing its usage line %q: %q", want, topic)
	}
	for _, child := range download.children {
		if !topicRow(topic, child) {
			t.Errorf("download topic must list the %s subcommand (%q): %q", child.name, child.summary, topic)
		}
	}
	for _, opt := range config.SubdirOptions {
		if !strings.Contains(topic, "--subdir-"+opt.Name) {
			t.Errorf("download topic is missing --subdir-%s: %q", opt.Name, topic)
		}
		for _, template := range opt.Templates {
			if !strings.Contains(topic, template) {
				t.Errorf("download topic must name the %s template for --subdir-%s: %q", template, opt.Name, topic)
			}
		}
	}
	// The aggregate contract belongs in the topic a reader trusts before
	// running a bulk transfer: the node's own notes.
	for _, note := range nodeNotes(t, "download") {
		if !strings.Contains(topic, note) {
			t.Errorf("download topic must state %q: %q", note, topic)
		}
	}

	file := topicNode(t, "download", "file")
	_, fileTopic, _ := run(t, "", "download", "file", "-h")
	if want := commandUsageLine(t, "download", "file"); !strings.Contains(fileTopic, want) {
		t.Errorf("download file topic is missing its usage line %q: %q", want, fileTopic)
	}
	for _, note := range nodeNotes(t, "download", "file") {
		if !strings.Contains(fileTopic, note) {
			t.Errorf("download file topic must state %q: %q", note, fileTopic)
		}
	}
	for _, long := range nodeOptionLongs(file) {
		if !strings.Contains(fileTopic, long) {
			t.Errorf("download file topic is missing %s: %q", long, fileTopic)
		}
	}
}
