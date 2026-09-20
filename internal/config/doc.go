// Package config defines the configuration data model and the shared option
// tables used across goggo.
//
// A Config is a plain value: callers construct it explicitly and pass it down,
// rather than mutating process-wide state. The type itself assigns no defaults;
// NewConfig fills in the ones owned by the config domain, and the CLI sets the
// rest as it parses options.
package config
