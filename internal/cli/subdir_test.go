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
	inv := mustParse(t, "backup", "download", "g")
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
	inv = mustParse(t, "backup", "download", "g", "--installer-platform", "mac")
	if inv.cfg.DownloadConfig.InstallerPlatform != config.PlatformMac {
		t.Errorf("--installer-platform mac = %d, want mac only", inv.cfg.DownloadConfig.InstallerPlatform)
	}
}

// TestParseRemoteXMLDefault locks the other default: remote XML is on — without
// it the installer/patch version check silently never runs.
func TestParseRemoteXMLDefault(t *testing.T) {
	inv := mustParse(t, "backup", "download", "g")
	if !inv.cfg.DownloadConfig.RemoteXML {
		t.Error("RemoteXML = false, want the default true")
	}
}

// TestSubdirDefaults locks the values applyParseDefaults writes for the six
// domains: the parsed default is what the download path actually uses, so this
// is the CLI's half of the config table's defaults. The expected values are the
// table's own, so a change of default is a change to config, not to the test.
func TestSubdirDefaults(t *testing.T) {
	inv := mustParse(t, "backup", "download", "some_game")
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
			inv := mustParse(t, "backup", "download", "g", "--subdir-"+name, v)
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
	// backup download accepts all six.
	for _, base := range [][]string{{"backup", "download", "g"}, {"backup", "download", "g", "1"}} {
		for _, name := range []string{"installers", "extras", "patches", "language-packs", "dlc", "game"} {
			if _, err := parseArgs(append(append([]string{}, base...), "--subdir-"+name, "x"), config.Config{}); err != nil {
				t.Errorf("%v --subdir-%s: %v", base, name, err)
			}
		}
	}
}

// TestBackupDownloadDualStateResolution locks the dual-state resolution:
// "backup download <game>" is full game download, "backup download <game> [<file>...]" is specific file selector(s).
func TestBackupDownloadDualStateResolution(t *testing.T) {
	inv := mustParse(t, "backup", "download", "terraria")
	if inv.cmd != cmdBackupDownload || len(inv.args) != 1 || inv.args[0] != "terraria" {
		t.Errorf("backup download terraria = cmd %v args %v", inv.cmd, inv.args)
	}
	inv = mustParse(t, "backup", "download", "terraria", "123")
	if inv.cmd != cmdBackupDownload || len(inv.args) != 2 {
		t.Errorf("backup download single file = cmd %v args %v", inv.cmd, inv.args)
	}
	inv = mustParse(t, "backup", "download", "a", "b", "c")
	if inv.cmd != cmdBackupDownload || len(inv.args) != 3 {
		t.Errorf("variadic files = %v", inv.args)
	}
	inv = mustParse(t, "backup", "download", "terraria/123")
	if inv.cmd != cmdBackupDownload || len(inv.args) != 1 || inv.args[0] != "terraria/123" {
		t.Errorf("backup download shorthand = cmd %v args %v", inv.cmd, inv.args)
	}
	// The download leaf keeps the no-argument refusal.
	err := mustUsageError(t, "backup", "download")
	if !strings.Contains(err.Error(), "needs a game") {
		t.Errorf("bare backup download = %v, want the needs-a-game refusal", err)
	}
}

// TestOutputFileRules locks -o: only backup download with a single file selector (<game> <fileid> or <game>/<fileid>) accepts it,
// and the value is kept raw for the dispatcher (which refuses directories).
func TestOutputFileRules(t *testing.T) {
	inv := mustParse(t, "backup", "download", "g", "1", "-o", "out.zip")
	if inv.outputFile != "out.zip" {
		t.Errorf("-o = %q, want it stored", inv.outputFile)
	}
	inv = mustParse(t, "backup", "download", "g/1", "-o", "out.zip")
	if inv.outputFile != "out.zip" {
		t.Errorf("-o with slash spec = %q, want it stored", inv.outputFile)
	}
	err := mustUsageError(t, "backup", "download", "g", "1", "2", "-o", "out.zip")
	if !strings.Contains(err.Error(), "exactly one file") {
		t.Errorf("-o with three args = %v, want the parser refusal", err)
	}
	err = mustUsageError(t, "backup", "download", "g", "-o", "out.zip")
	if !strings.Contains(err.Error(), "exactly one file") {
		t.Errorf("-o on full game download = %v, want the refusal", err)
	}
	err = mustUsageError(t, "backup", "download", "g", "--type", "extras", "-o", "out.zip")
	if !strings.Contains(err.Error(), "exactly one file") {
		t.Errorf("-o with --type = %v, want the refusal", err)
	}
}

// TestBackupDownloadTopicNamesTemplates applies the rule that a whitelist the
// user cannot read is a whitelist the user cannot use to the six subdir domains:
// each option's help must list exactly its own templates.
func TestBackupDownloadTopicNamesTemplates(t *testing.T) {
	download := topicNode(t, "backup", "download")
	_, topic, _ := run(t, "", "backup", "download", "-h")

	if want := commandUsageLine(t, "backup", "download"); !strings.Contains(topic, want) {
		t.Errorf("backup download topic is missing its usage line %q: %q", want, topic)
	}
	for _, opt := range config.SubdirOptions {
		if !strings.Contains(topic, "--subdir-"+opt.Name) {
			t.Errorf("backup download topic is missing --subdir-%s: %q", opt.Name, topic)
		}
		for _, template := range opt.Templates {
			if !strings.Contains(topic, template) {
				t.Errorf("backup download topic must name the %s template for --subdir-%s: %q", template, opt.Name, topic)
			}
		}
	}
	for _, note := range nodeNotes(t, "backup", "download") {
		if !strings.Contains(topic, note) {
			t.Errorf("backup download topic must state %q: %q", note, topic)
		}
	}
	for _, long := range nodeOptionLongs(download) {
		if !strings.Contains(topic, long) {
			t.Errorf("backup download topic is missing %s: %q", long, topic)
		}
	}
}
