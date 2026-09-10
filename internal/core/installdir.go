package core

import (
	"errors"
	"fmt"

	"github.com/nekrozis/goggo/internal/jsonval"
	"github.com/nekrozis/goggo/internal/util"
)

// ErrUnsupportedTemplate reports an install subdirectory template this build
// cannot resolve because it needs product information
// (galaxyAPI::getProductInfo), which is not ported yet.
var ErrUnsupportedTemplate = errors.New("install subdir template is not supported in this build")

// ResolveInstallSubdir expands one install subdirectory template
// (Downloader::getGalaxyInstallDirectory, downloader.cpp:6659-6700).
//
// The template is matched WHOLE: it must be exactly one of the names below, and
// anything else comes back unchanged. That is what the C++ source does — it
// looks the value up in a map instead of substituting placeholders inside a
// longer path — so "--subdir-galaxy-install %install_dir%/data" keeps its
// literal text rather than gaining an expanded prefix.
//
//	%install_dir%          manifest.installDirectory
//	%product_id%           manifest.baseProductId, not the requested id
//	%install_dir_stripped% %install_dir% with everything but letters, digits,
//	                       spaces and - _ . ( ) [ ] { } removed
//	%gamename%             needs getProductInfo -> ErrUnsupportedTemplate
//	%title%                needs getProductInfo -> ErrUnsupportedTemplate
//	%title_stripped%       needs getProductInfo -> ErrUnsupportedTemplate
//
// The bSubDirectories test belongs to the caller: the C++ source checks it
// before calling this at all (downloader.cpp:4078-4081), and installing without
// subdirectories leaves install_directory empty.
func ResolveInstallSubdir(template string, manifest map[string]any) (string, error) {
	switch template {
	case "%install_dir%":
		return manifestString(manifest, "installDirectory")
	case "%product_id%":
		return manifestString(manifest, "baseProductId")
	case "%install_dir_stripped%":
		// The C++ source derives this from the %install_dir% entry, which is
		// always present in its map.
		dir, err := manifestString(manifest, "installDirectory")
		if err != nil {
			return "", err
		}
		return util.StrippedString(dir), nil
	case "%gamename%", "%title%", "%title_stripped%":
		return "", fmt.Errorf("%w: %s", ErrUnsupportedTemplate, template)
	default:
		// Not a name this function knows, so the map lookup finds nothing and
		// the value stays what the caller passed.
		return template, nil
	}
}

// manifestString reads one manifest member as a string: a missing member or a
// null one is the empty string, and a structured one is an error rather than
// the crash jsoncpp's asString() would raise. That contract matches the other
// document readers in this port.
func manifestString(manifest map[string]any, key string) (string, error) {
	v, err := jsonval.Str(manifest[key])
	if err != nil {
		return "", fmt.Errorf("galaxy: manifest %s: %w", key, err)
	}
	return v, nil
}
