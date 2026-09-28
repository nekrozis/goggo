package core

import (
	"github.com/nekrozis/goggo/internal/config"
)

// InstallRequest is one Galaxy install request, resolved from the options into
// values: a raw command-line string never travels further than the parser —
// except Language, which the plan has to resolve against the build it fetches.
//
// It carries the install subdirectory TEMPLATE rather than a resolved
// directory. %install_dir% comes from the build manifest, which is fetched
// during the install itself, so the template is resolved there; keeping the two
// apart is what makes the request describable before any request is made.
type InstallRequest struct {
	ProductID string
	BuildID   string
	Platform  string

	// Language is the --language value exactly as typed. The empty string is
	// the flag's absence and nothing else — the option requires a value — and
	// the plan then resolves the default request instead. Which languages the
	// build can satisfy is only known once its manifest is fetched, so the
	// resolution happens there, not here.
	Language string

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
// applied, and none of them is a command-line string except Language: the front
// end parses the options, it does not decide what the "windows" platform
// segment is, nor which language the build actually offers.
func NewInstallRequest(cfg config.Config, productID, buildID string, refMode ProductRefMode) InstallRequest {
	download := cfg.DownloadConfig
	return InstallRequest{
		ProductID:      productID,
		BuildID:        buildID,
		Platform:       platformName(download.GalaxyPlatform),
		Language:       download.GalaxyLanguageRaw,
		SubdirTemplate: cfg.Directories.GalaxyInstallSubdir,

		Arch:                download.GalaxyArch,
		IncludeDependencies: download.GalaxyDependencies,

		RefMode: refMode,
	}
}
