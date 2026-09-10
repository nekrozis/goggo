package galaxy

import "context"

// The upstream default arguments of getProductBuilds (galaxyapi.h:58). They are
// applied here rather than exported, because nothing outside this package picks
// a platform yet: the caller that does (the download path, S17) will decide
// then whether to reuse these or spell its own values.
const (
	defaultPlatform   = "windows"
	defaultGeneration = "2"
)

// ProductBuilds fetches the builds document for a product on a platform
// (galaxyapi.cpp:190-195).
//
// An empty platform or generation falls back to the upstream default argument.
// The URL is concatenated exactly the way the C++ source concatenates it, with
// no encoding: product ids and platform names are already URL-safe, and
// re-encoding would change the request the original makes.
//
// The document is returned as decoded JSON. The C++ caller navigates it itself
// (downloader.cpp:4043-4076 reads items[].generation and items[].link, and sorts
// the array before doing so), so this port imposes no schema of its own and
// leaves the field-level reading to the step that consumes it.
func (c *Client) ProductBuilds(ctx context.Context, productID, platform, generation string) (map[string]any, error) {
	if platform == "" {
		platform = defaultPlatform
	}
	if generation == "" {
		generation = defaultGeneration
	}
	target := c.ep.contentSystem + "/products/" + productID + "/os/" + platform + "/builds?generation=" + generation
	return c.getResponseJSON(ctx, target)
}
