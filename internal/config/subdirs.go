package config

import (
	"slices"
	"strings"
)

// This file is the single source of truth for the six website subdirectory
// options: the field each one fills, its default and the whole values carrying a
// placeholder that it accepts. The CLI parser and the help text both read this
// table, so the whitelist cannot drift from it.
//
// These values are expanded by gamedetails.makeFilepath's placeholder pass, not by
// the install-dir resolver: the two template families share no table, and a
// placeholder is accepted only when it renders meaningfully for the field's file
// class, and only as a WHOLE value. The transformed-gamename placeholders are
// absent because their backing transformations JSON is not implemented.

// SubdirOption describes one --subdir-* option.
type SubdirOption struct {
	// Set writes the parsed value into its DirectoryConfig field.
	Set func(conf *DirectoryConfig, value string)
	// Name is the option's suffix: --subdir-<Name>.
	Name string
	// Default is the directory name used when the option is not given.
	Default string
	// Templates lists the whole values that may carry a placeholder for
	// this field. A value without '%' is a literal directory name and is
	// accepted whatever this list says.
	Templates []string
}

// SubdirOptions are the six website subdirectory domains.
var SubdirOptions = []SubdirOption{
	{
		Name:      "installers",
		Default:   "",
		Templates: []string{"%platform%", "%version%"},
		Set:       func(c *DirectoryConfig, v string) { c.InstallersSubdir = v },
	},
	{
		Name:      "extras",
		Default:   "extras",
		Templates: []string{"%platform%", "%version%"},
		Set:       func(c *DirectoryConfig, v string) { c.ExtrasSubdir = v },
	},
	{
		Name:      "patches",
		Default:   "patches",
		Templates: []string{"%platform%", "%version%"},
		Set:       func(c *DirectoryConfig, v string) { c.PatchesSubdir = v },
	},
	{
		Name:      "language-packs",
		Default:   "languagepacks",
		Templates: []string{"%platform%", "%version%"},
		Set:       func(c *DirectoryConfig, v string) { c.LanguagePackSubdir = v },
	},
	{
		// The DLC default is itself a whole allowed value: a literal segment
		// plus one placeholder.
		Name:      "dlc",
		Default:   "dlc/%dlcname%",
		Templates: []string{"%dlcname%", "%dlc_title%", "%dlc_title_stripped%", "dlc/%dlcname%"},
		Set:       func(c *DirectoryConfig, v string) { c.DLCSubdir = v },
	},
	{
		Name:      "game",
		Default:   "%gamename%",
		Templates: []string{"%gamename%", "%gamename_firstletter%", "%title%", "%title_stripped%"},
		Set:       func(c *DirectoryConfig, v string) { c.GameSubdir = v },
	},
}

// SubdirValueAccepted reports whether value is legal for one domain: any
// literal without a placeholder passes; a value carrying '%' must be one of
// the domain's whole allowed values.
func SubdirValueAccepted(opt SubdirOption, value string) bool {
	if !strings.Contains(value, "%") {
		return true
	}
	return slices.Contains(opt.Templates, value)
}
