package galaxy

import (
	"context"
	"strings"
)

// ManifestV1 fetches a generation-1 manifest from its full URL.
//
// The builds document carries that URL verbatim in items[].link, so it is taken
// as given rather than rebuilt here.
func (c *Client) ManifestV1(ctx context.Context, manifestURL string) (map[string]any, error) {
	return c.getResponseJSON(ctx, manifestURL)
}

// ManifestV2 fetches a generation-2 manifest by hash.
//
// A non-empty hash is expanded to the content-system layout first, and a hash
// that already carries a "/" comes back unchanged from HashToGalaxyPath;
// isDependency selects the dependency repository instead of the product one.
//
// For a BARE hash the URL carries no ".json" suffix and no query string, unlike
// generation 1; a hash containing a "/" is interpolated as it is, query string
// included. An empty hash still produces a request (".../v2/meta/"), and the
// server's answer decides the outcome.
func (c *Client) ManifestV2(ctx context.Context, manifestHash string, isDependency bool) (map[string]any, error) {
	hash := manifestHash
	if hash != "" {
		// HashToGalaxyPath returns a hash containing "/" unchanged, so only the
		// empty case has to be tested here.
		hash = HashToGalaxyPath(hash)
	}

	var target string
	if isDependency {
		target = c.ep.cdn + "/content-system/v2/dependencies/meta/" + hash
	} else {
		target = c.ep.cdn + "/content-system/v2/meta/" + hash
	}
	return c.getResponseJSON(ctx, target)
}

// HashToGalaxyPath expands a bare hash into the content-system path layout: the
// first two characters, then the next two, then the whole hash — "ab/cd/abcd…".
//
// A hash that already contains a "/" is returned unchanged, and so is one shorter
// than four characters: it cannot be split, and returning it unchanged keeps it
// from becoming a slice panic (the request that follows reports the problem). It
// is exported because the download layer, outside this package, calls it on a
// chunk's compressed md5.
func HashToGalaxyPath(hash string) string {
	if strings.Contains(hash, "/") {
		return hash
	}
	if len(hash) < 4 {
		return hash
	}
	return hash[:2] + "/" + hash[2:4] + "/" + hash
}
