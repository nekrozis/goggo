package core

import (
	"fmt"

	"github.com/nekrozis/goggo/internal/jsonval"
	"github.com/nekrozis/goggo/internal/util"
)

// InstallSubdirTemplates is every install-subdirectory template this build
// resolves, in the order they are documented. It is the single source of truth
// for the resolver below and for the front end's --install-dir whitelist, so
// the two cannot drift apart.
var InstallSubdirTemplates = []string{
	"%install_dir%",
	"%product_id%",
	"%install_dir_stripped%",
	"%gamename%",
	"%title%",
	"%title_stripped%",
}

// IsInstallSubdirTemplate reports whether name is one of the known templates.
// A front end uses it to tell a template from a concrete directory name.
func IsInstallSubdirTemplate(name string) bool {
	for _, template := range InstallSubdirTemplates {
		if template == name {
			return true
		}
	}
	return false
}

// InstallSubdirNeedsProductInfo reports whether a template's value comes from
// the product document, so a caller can fetch it only when it is needed
// (downloader.cpp:6679-6691).
func InstallSubdirNeedsProductInfo(template string) bool {
	switch template {
	case "%gamename%", "%title%", "%title_stripped%":
		return true
	default:
		return false
	}
}

// ResolveInstallSubdir expands one install subdirectory template
// (Downloader::getGalaxyInstallDirectory, downloader.cpp:6659-6700).
//
// The template is matched WHOLE: it must be exactly one of the names below, and
// anything else comes back unchanged. That is what the C++ source does — it
// looks the value up in a map instead of substituting placeholders inside a
// longer path — so "--install-dir %install_dir%/data" keeps its literal text
// rather than gaining an expanded prefix.
//
//	%install_dir%          manifest.installDirectory
//	%product_id%           manifest.baseProductId, not the requested id
//	%install_dir_stripped% %install_dir% with everything but letters, digits,
//	                       spaces and - _ . ( ) [ ] { } removed
//	%gamename%             product.slug
//	%title%                product.title
//	%title_stripped%       %title% stripped, registered only with %title%
//
// product is the document of manifest.baseProductId, or nil when the caller
// did not need it (see InstallSubdirNeedsProductInfo) or could not name the
// product. The three templates that read it are registered only when the
// document actually carries a non-empty value, so a missing or empty slug or
// title leaves the NAME ITSELF as the result: "%title%/setup" must not collapse
// to "/setup" (review GD3, ruling D). The two stripped names follow upstream's
// key test rather than a value test — %title_stripped% appears exactly when
// %title% does, so it is a literal again when there is no title.
//
// The lookup-miss rule is the C++ map's: a name that was never registered falls
// through unchanged. That covers an unknown name, a template embedded in a
// longer path, and the empty-value cases above.
//
// This function is pure — it issues no request — which is why the predicate
// above exists next to it (review GD3 §3.1): the caller decides whether to
// fetch the document, and this function only reads it. The caller also owns the
// bSubDirectories test the C++ source performs before calling this at all
// (downloader.cpp:4078-4081); installing without subdirectories leaves
// install_directory empty.
func ResolveInstallSubdir(template string, manifest, product map[string]any) (string, error) {
	installDir, err := documentString(manifest, "installDirectory")
	if err != nil {
		return "", err
	}
	// The C++ source reads baseProductId before it checks anything, so a
	// malformed one is an error whatever the template turns out to be.
	productID, err := documentString(manifest, "baseProductId")
	if err != nil {
		return "", err
	}

	name := map[string]string{
		"%install_dir%": installDir,
		"%product_id%":  productID,
		// The C++ source derives this from the %install_dir% entry, which is
		// always present in its map.
		"%install_dir_stripped%": util.StrippedString(installDir),
	}

	if InstallSubdirNeedsProductInfo(template) && product != nil {
		slug, err := documentString(product, "slug")
		if err != nil {
			return "", err
		}
		if slug != "" {
			name["%gamename%"] = slug
		}
		title, err := documentString(product, "title")
		if err != nil {
			return "", err
		}
		if title != "" {
			name["%title%"] = title
		}
		if title, ok := name["%title%"]; ok {
			name["%title_stripped%"] = util.StrippedString(title)
		}
	}

	if value, ok := name[template]; ok {
		return value, nil
	}
	// Not a name the map knows: the lookup finds nothing and the value stays
	// what the caller passed.
	return template, nil
}

// documentString reads one document member as a string: a missing member or a
// null one is the empty string, and a structured one is an error rather than
// the crash jsoncpp's asString() would raise. That contract matches the other
// document readers in this port, and it serves both the manifest and the
// product document the install directory is derived from.
func documentString(doc map[string]any, key string) (string, error) {
	v, err := jsonval.Str(doc[key])
	if err != nil {
		return "", fmt.Errorf("galaxy: document %s: %w", key, err)
	}
	return v, nil
}
