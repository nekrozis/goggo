package core

import (
	"fmt"

	"encoding/json/jsontext"
	"github.com/nekrozis/goggo/internal/jsonread"
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
// the product document, so a caller can fetch it only when it is needed.
func InstallSubdirNeedsProductInfo(template string) bool {
	switch template {
	case "%gamename%", "%title%", "%title_stripped%":
		return true
	default:
		return false
	}
}

// ResolveInstallSubdir expands one install subdirectory template.
//
// The template is matched WHOLE: it must be exactly one of the names in
// InstallSubdirTemplates, so "--install-dir %install_dir%/data" keeps its
// literal text rather than gaining an expanded prefix, and %product_id% is the
// manifest's baseProductId rather than the id the caller requested. product may
// be nil: a template that reads it is registered only when the document carries
// a non-empty value, so a missing slug or title leaves the NAME ITSELF as the
// result — "%title%/setup" must not collapse to "/setup". The function issues
// no request; the caller decides whether to fetch the document.
func ResolveInstallSubdir(template string, manifest, product map[string]jsontext.Value) (string, error) {
	installDir, err := documentString(manifest, "installDirectory")
	if err != nil {
		return "", err
	}
	// baseProductId is read before anything else, so a malformed one is an
	// error whatever the template turns out to be.
	productID, err := documentString(manifest, "baseProductId")
	if err != nil {
		return "", err
	}

	name := map[string]string{
		"%install_dir%": installDir,
		"%product_id%":  productID,
		// Derived from the %install_dir% entry, which is always present here.
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
// null one is the empty string, and a structured one is an error. That contract
// matches the other document readers in this package, and it serves both the
// manifest and the product document the install directory is derived from.
func documentString(doc map[string]jsontext.Value, key string) (string, error) {
	// Any scalar is rendered as its own text: a path template must not be the
	// place a document gets rejected, and baseProductId really is an id that the
	// API may spell as a number.
	v, err := jsonread.Scalar(doc[key])
	if err != nil {
		return "", fmt.Errorf("galaxy: document %s: %w", key, err)
	}
	return v, nil
}
