package galaxy

import (
	"context"
	"strings"
)

// ManifestV1 fetches a generation-1 manifest from its full URL
// (galaxyapi.cpp:204-207).
//
// The builds document carries that URL verbatim in items[].link, and the C++
// caller passes it straight through (downloader.cpp:4919), so this port takes
// the URL as given rather than rebuilding it.
//
// The four-argument overload of the C++ class (galaxyapi.cpp:197-202, which
// composes ".../content-system/v1/manifests/<product>/<platform>/<build>/<id>.json")
// has no caller anywhere in the upstream sources, so it is not ported: this
// port carries the surface that is used, not every public overload of the C++
// class.
func (c *Client) ManifestV1(ctx context.Context, manifestURL string) (map[string]any, error) {
	return c.getResponseJSON(ctx, manifestURL)
}

// ManifestV2 fetches a generation-2 manifest by hash (galaxyapi.cpp:209-221).
//
// A non-empty hash is expanded to the content-system layout first; a hash that
// already carries a "/" comes back unchanged from hashToGalaxyPath, and an
// empty one is left alone, exactly as the C++ guard leaves it. isDependency
// selects the dependency repository instead of the product one.
//
// Note the URL has no ".json" suffix and no query string, unlike generation 1,
// and that an empty hash still produces a request: it goes to
// ".../v2/meta/" and the server's answer decides the outcome (upstream
// behaviour as written, kept deliberately).
func (c *Client) ManifestV2(ctx context.Context, manifestHash string, isDependency bool) (map[string]any, error) {
	hash := manifestHash
	if hash != "" {
		// One check, not two: the C++ version guards on "non-empty AND no
		// slash" and then calls a helper that tests for a slash again
		// (galaxyapi.cpp:211-212 with 244-251). The helper returns a slashed
		// hash unchanged, so only the empty case has to be tested here. An
		// equivalent simplification, not a behaviour change.
		hash = hashToGalaxyPath(hash)
	}

	var target string
	if isDependency {
		target = c.ep.cdn + "/content-system/v2/dependencies/meta/" + hash
	} else {
		target = c.ep.cdn + "/content-system/v2/meta/" + hash
	}
	return c.getResponseJSON(ctx, target)
}

// hashToGalaxyPath expands a bare hash into the content-system path layout
// (galaxyapi.cpp:244-251): the first two characters, then the next two, then
// the whole hash — "ab" + "/" + "cd" + "/" + "abcd…".
//
// A hash that already contains a "/" is returned unchanged.
//
// The C++ version slices unconditionally, which is undefined behaviour for a
// hash shorter than four characters (hash.begin()+4 runs past the end). Go must
// not turn that into a slice panic, so a short hash comes back unchanged: it
// cannot be split, and the request that follows is what reports a problem. The
// error channel is deliberately not widened for a case upstream does not
// define.
//
// It stays unexported for now: the only caller here is ManifestV2. The other
// upstream caller is the download layer (downloader.cpp:4622), which arrives in
// S15 — that is when the name gets exported.
func hashToGalaxyPath(hash string) string {
	if strings.Contains(hash, "/") {
		return hash
	}
	if len(hash) < 4 {
		return hash
	}
	return hash[:2] + "/" + hash[2:4] + "/" + hash
}
