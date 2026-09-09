// Package config defines the configuration data model and the shared option
// tables used across goggo.
//
// It is a Go port of the following LGOGDownloader sources
// (https://github.com/Sude-/lgogdownloader, WTFPL; pinned reference under
// /reference):
//
//	include/config.h          -> types.go, galaxyconfig.go
//	include/globalconstants.h -> constants.go, options.go
//	include/globals.h         -> removed by design
//
// include/globals.h declared mutable process-wide singletons (Globals::Config,
// Globals::galaxyConf, ...). Go code instead constructs Config values
// explicitly and passes them down, which keeps dependency flow visible and
// testable; the CLI layer (internal/cli) owns construction.
//
// Defaults are intentionally NOT assigned here: the C++ program wires them in
// main.cpp (version/UA/paths at main.cpp:67-84, CLI option defaults and the
// clamps at main.cpp:299-330 and main.cpp:519-527). internal/cli will set
// them through NewConfig() when option parsing lands.
package config
