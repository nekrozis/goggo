package galaxy

import (
	"context"
	"errors"
	"fmt"
)

// SecureLink fetches the CDN link document that a product's chunks are served
// from.
//
// The path is interpolated without URL encoding: the download path passes "/"
// here and builds the rest from the document, and encoding would produce a
// different request.
func (c *Client) SecureLink(ctx context.Context, productID, path string) (map[string]any, error) {
	target := c.ep.contentSystem + "/products/" + productID +
		"/secure_link?generation=2&path=" + path + "&_version=2"
	return c.getResponseJSON(ctx, target)
}

// DependencyLink fetches the CDN link document for one dependency path. The
// path is the already-expanded galaxy path, and it is interpolated without
// encoding for the same reason as SecureLink's.
func (c *Client) DependencyLink(ctx context.Context, path string) (map[string]any, error) {
	target := c.ep.contentSystem + "/open_link?generation=2&_version=2&path=/dependencies/store/" + path
	return c.getResponseJSON(ctx, target)
}

// DependenciesJSON fetches the dependency repository document. It is a
// two-step fetch: the repository endpoint names a manifest URL, and that second
// document is what the caller wants.
//
// "No repository" is a normal result rather than a failure: an empty or
// non-object repository document (this package's ErrNotJSON) and a missing
// "repository_manifest" both yield an empty document and no error, and a
// manifest URL that reads as "" skips the second request — which keeps "there is
// no manifest" distinguishable from "the request failed".
//
// Any other failure is returned: an HTTP or transport error on either step, or a
// "repository_manifest" that is not a scalar.
func (c *Client) DependenciesJSON(ctx context.Context) (map[string]any, error) {
	repository, err := c.getResponseJSON(ctx, c.ep.contentSystem+"/dependencies/repository?generation=2")
	if err != nil {
		if errors.Is(err, ErrNotJSON) {
			return map[string]any{}, nil
		}
		return nil, err
	}

	manifestURL, err := scalarString(repository["repository_manifest"])
	if err != nil {
		return nil, fmt.Errorf("galaxy: dependencies: repository_manifest: %w", err)
	}
	if manifestURL == "" {
		return map[string]any{}, nil
	}
	return c.getResponseJSON(ctx, manifestURL)
}
