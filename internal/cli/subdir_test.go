package cli

import (
	"strings"
	"testing"

	"github.com/nekrozis/goggo/internal/config"
)

// GD4 3.3: the six --subdir-* options, their upstream defaults and the
// per-field whole-template whitelist. Each domain is its own language - the
// install-dir table (GD3) must not leak into these, and these must not leak
// into it.

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

// TestParseInstallerGateDefaults locks the DEFECT-GD4-1 fix: the conversion's
// installer platform/language gate gets the upstream front-end defaults
// ("w+l" and "en", main.cpp:280-281), parsed into both the mask and the
// priority list the way Util::parseOptionString does — without them every
// non-extras vector silently dropped from a download run.
func TestParseInstallerGateDefaults(t *testing.T) {
	inv := mustParse(t, "download", "g")
	if inv.cfg.DownloadConfig.InstallerPlatform != config.PlatformWindows|config.PlatformLinux {
		t.Errorf("installer platform = %d, want windows+linux", inv.cfg.DownloadConfig.InstallerPlatform)
	}
	if inv.cfg.DownloadConfig.InstallerLanguage != config.LangEN {
		t.Errorf("installer language = %d, want en", inv.cfg.DownloadConfig.InstallerLanguage)
	}
	// "w+l" is ONE comma-group: the priority list carries one entry with both
	// bits, exactly what Util::parseOptionString builds upstream.
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

// TestParseRemoteXMLDefault locks the other half of the GD4 front-end
// defaults: remote XML is on, the way upstream's bRemoteXML = !bNoRemoteXML
// (main.cpp:282,541) declares it — without it the installer/patch version
// check silently never runs.
func TestParseRemoteXMLDefault(t *testing.T) {
	inv := mustParse(t, "download", "g")
	if !inv.cfg.DownloadConfig.RemoteXML {
		t.Error("RemoteXML = false, want the upstream default true")
	}
}

// TestSubdirDefaults locks the values applyParseDefaults writes: the upstream
// boost default_values (main.cpp:293-298), read through the config table.
func TestSubdirDefaults(t *testing.T) {
	inv := mustParse(t, "download", "some_game")
	want := map[string]string{
		"installers":     "",
		"extras":         "extras",
		"patches":        "patches",
		"language-packs": "languagepacks",
		"dlc":            "dlc/%dlcname%",
		"game":           "%gamename%",
	}
	for name, expected := range want {
		opt, ok := config.SubdirOptionByName(name)
		if !ok {
			t.Fatalf("config table lost the %q domain", name)
		}
		if opt.Default != expected {
			t.Errorf("config default for %s = %q, want %q", name, opt.Default, expected)
		}
		if got := subdirField(inv.cfg.Directories, name); got != expected {
			t.Errorf("parsed default --subdir-%s = %q, want %q", name, got, expected)
		}
	}
}

// TestSubdirWhitelistIsPerField locks the independent domains: each option
// accepts any literal and only its own whole templates; a template belonging
// to another domain, an embedded placeholder or a half-spelled template is a
// usage error (GD4 Gate 1 ruling 3).
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

// TestSubdirOptionsAreDownloadOnly: the six options belong to the download
// commands; install keeps its own directory vocabulary (GD4 Gate 1 ruling 3).
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

// TestDownloadDualStateResolution locks the frozen surface (GD4 ruling 9):
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
	// download exists (GD4 ruling 2).
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
// dispatcher (which refuses directories) (GD4 Gate 1 ruling 5).
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

// TestDownloadTopicsNameTheirTemplates extends the GD3 rule (a whitelist the
// user cannot read is a whitelist the user cannot use) to the six subdir
// domains: each option's help must list exactly its own templates.
func TestDownloadTopicsNameTheirTemplates(t *testing.T) {
	_, topic, _ := run(t, "", "download", "-h")
	if !strings.Contains(topic, "Usage: goggo download <game>...") {
		t.Errorf("download topic is missing the variadic usage line: %q", topic)
	}
	if !strings.Contains(topic, "file") {
		t.Errorf("download topic must list the file subcommand: %q", topic)
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
	// running a bulk transfer.
	if !strings.Contains(topic, "exits 1") {
		t.Errorf("download topic must state the aggregate exit: %q", topic)
	}

	_, fileTopic, _ := run(t, "", "download", "file", "-h")
	for _, want := range []string{"Usage: goggo download file <spec>...", "--output-file", "gogdownloader://", "fileid"} {
		if !strings.Contains(fileTopic, want) {
			t.Errorf("download file topic is missing %s: %q", want, fileTopic)
		}
	}
}
