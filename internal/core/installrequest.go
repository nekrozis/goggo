package core

import (
	"github.com/nekrozis/goggo/internal/config"
	"github.com/nekrozis/goggo/internal/util"
)

// defaultLanguageRegex is the expression the Galaxy depot filter falls back to
// when the selected language flag matches no table entry.
const defaultLanguageRegex = "en|eng|english|en[_-]US"

// InstallRequest is one Galaxy install request, resolved from the options into
// values: a raw command-line string never travels further than the parser.
//
// It carries the install subdirectory TEMPLATE rather than a resolved
// directory. %install_dir% comes from the build manifest, which is fetched
// during the install itself, so the template is resolved there; keeping the two
// apart is what makes the request describable before any request is made.
type InstallRequest struct {
	ProductID      string
	BuildID        string
	Platform       string
	LanguageRegex  string
	SubdirTemplate string

	Arch                uint32
	IncludeDependencies bool

	// RefMode says how ProductID is read. It travels with the request because
	// the same reference means two different things under --regex, and nothing
	// downstream may re-derive which one the user asked for.
	RefMode ProductRefMode
}

// NewInstallRequest resolves the effective configuration into a request.
//
// Every value comes from cfg, where the option defaults have already been
// applied, and none of them is a command-line string: the front end parses the
// options, it does not decide what the English language expression or the
// "windows" platform segment are.
func NewInstallRequest(cfg config.Config, productID, buildID string, refMode ProductRefMode) InstallRequest {
	download := cfg.DownloadConfig
	return InstallRequest{
		ProductID:      productID,
		BuildID:        buildID,
		Platform:       platformName(download.GalaxyPlatform),
		LanguageRegex:  languageRegex(download.GalaxyLanguage),
		SubdirTemplate: cfg.Directories.GalaxyInstallSubdir,

		Arch:                download.GalaxyArch,
		IncludeDependencies: download.GalaxyDependencies,

		RefMode: refMode,
	}
}

// languageRegex maps a Galaxy language flag onto the expression the depot
// filter uses. A flag that matches no entry — which is what an unrecognised
// --galaxy-language leaves behind — falls back to the English expression.
func languageRegex(flag uint32) string {
	if o, ok := util.OptionByID(flag, config.Languages); ok {
		return o.Regexp
	}
	return defaultLanguageRegex
}
