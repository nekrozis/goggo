package galaxy

import "context"

// Default platform and generation for ProductBuilds, applied when the caller
// passes an empty value. They are unexported because nothing outside this
// package picks a platform yet: the caller that does will decide then whether
// to reuse these or spell its own values.
const (
	defaultPlatform   = "windows"
	defaultGeneration = "2"
)

// ProductBuilds fetches the builds document for a product on a platform.
//
// An empty platform or generation falls back to the package defaults. The URL
// is concatenated without encoding: product ids and platform names are already
// URL-safe, and re-encoding would change the request.
//
// The document is returned as decoded JSON with no schema of its own: the
// caller navigates items[].generation and items[].link itself, and sorts the
// array before doing so.
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
