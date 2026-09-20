package config

import "strings"

// This file is the single source of truth for the six website subdirectory
// options: the field each one fills, its default and the whole values carrying
// a placeholder that it accepts. The CLI parser and the help text both read this
// table, so the whitelist cannot drift from it.
//
// The subdir template family and the install-dir template family are two
// DIFFERENT languages and deliberately share no table:
// --install-dir resolves through core.ResolveInstallSubdir (a document-backed
// lookup), while these values are expanded by gamedetails.makeFilepath's
// placeholder pass over each file's own fields. Only placeholders that render
// meaningfully for the file class of a field are accepted by that field, and
// only as a WHOLE value — a placeholder embedded in a longer path is refused,
// exactly like the install-dir whitelist refuses half-exposed templates the
// resolver would keep literal. The transformed-gamename placeholders are
// absent everywhere: their backing transformations JSON is not implemented, so
// they would render empty.

// SubdirOption describes one --subdir-* option.
type SubdirOption struct {
	// Name is the option's suffix: --subdir-<Name>.
	Name string
	// Default is the directory name used when the option is not given.
	Default string
	// Templates lists the whole values that may carry a placeholder for
	// this field. A value without '%' is a literal directory name and is
	// accepted whatever this list says.
	Templates []string
	// Set writes the parsed value into its DirectoryConfig field.
	Set func(conf *DirectoryConfig, value string)
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

// SubdirOptionByName finds one domain by its option suffix.
func SubdirOptionByName(name string) (SubdirOption, bool) {
	for _, opt := range SubdirOptions {
		if opt.Name == name {
			return opt, true
		}
	}
	return SubdirOption{}, false
}

// SubdirValueAccepted reports whether value is legal for one domain: any
// literal without a placeholder passes; a value carrying '%' must be one of
// the domain's whole allowed values.
func SubdirValueAccepted(opt SubdirOption, value string) bool {
	if !strings.Contains(value, "%") {
		return true
	}
	for _, template := range opt.Templates {
		if template == value {
			return true
		}
	}
	return false
}
