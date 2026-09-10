package galaxy

import (
	"context"
	"errors"
	"fmt"

	"github.com/nekrozis/goggo/internal/jsonval"
)

// SecureLink fetches the CDN link document that a product's chunks are served
// from (galaxyapi.cpp:229-234).
//
// The path is interpolated exactly the way the C++ source interpolates it: no
// URL encoding. The download path passes "/" here and builds the rest from the
// document, and encoding would produce a different request than the original
// makes.
func (c *Client) SecureLink(ctx context.Context, productID, path string) (map[string]any, error) {
	target := c.ep.contentSystem + "/products/" + productID +
		"/secure_link?generation=2&path=" + path + "&_version=2"
	return c.getResponseJSON(ctx, target)
}

// DependencyLink fetches the CDN link document for one dependency path
// (galaxyapi.cpp:236-241). The path is the already-expanded galaxy path, and it
// is interpolated without encoding for the same reason as SecureLink's.
func (c *Client) DependencyLink(ctx context.Context, path string) (map[string]any, error) {
	target := c.ep.contentSystem + "/open_link?generation=2&_version=2&path=/dependencies/store/" + path
	return c.getResponseJSON(ctx, target)
}

// DependenciesJSON fetches the dependency repository document
// (galaxyapi.cpp:608-624). It is a two-step fetch: the repository endpoint names
// a manifest URL, and that second document is what the caller wants.
//
// "No repository" is a normal result rather than a failure, reported the way the
// C++ caller tests it (`!repository.empty()`, galaxyapi.cpp:614):
//
//   - a repository document that is empty or not a JSON object — this port's
//     ErrNotJSON — yields an empty document and no error;
//   - so does a document whose "repository_manifest" is absent.
//
// A "repository_manifest" that coerces to "" also yields an empty document, and
// the second request is skipped: upstream would request the empty URL that
// results, and not sending it keeps "there is no manifest" distinguishable from
// "the request failed".
//
// Everything else is a real fetch failure and is returned: an HTTP or transport
// error on either step, a "repository_manifest" that is not a scalar (where the
// C++ source would throw on asString), and any failure of the manifest request
// itself.
func (c *Client) DependenciesJSON(ctx context.Context) (map[string]any, error) {
	repository, err := c.getResponseJSON(ctx, c.ep.contentSystem+"/dependencies/repository?generation=2")
	if err != nil {
		if errors.Is(err, ErrNotJSON) {
			return map[string]any{}, nil
		}
		return nil, err
	}

	manifestURL, err := jsonval.Str(repository["repository_manifest"])
	if err != nil {
		return nil, fmt.Errorf("galaxy: dependencies: repository_manifest: %w", err)
	}
	if manifestURL == "" {
		return map[string]any{}, nil
	}
	return c.getResponseJSON(ctx, manifestURL)
}
