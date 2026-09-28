package core

import (
	"context"
	"fmt"
	"strings"

	"github.com/nekrozis/goggo/internal/galaxy"
	"github.com/nekrozis/goggo/internal/model"
)

// chunkURLProvider resolves the URL of one chunk through the Galaxy CDN
// machinery. It carries no state of its own: the run's link resolver owns the
// per-product secure link, the CDN templates and the token refresh, so the
// plan's entitlement probe and the transfer share one request per product.
type chunkURLProvider struct {
	links *linkResolver
}

// URL implements transfer.URLProvider.
func (p *chunkURLProvider) URL(ctx context.Context, task model.FileTask, chunk model.GalaxyDepotItemChunk) (string, error) {
	galaxyPath := galaxy.HashToGalaxyPath(chunk.CompressedMD5)

	// Dependencies re-resolve the link for every chunk and substitute an empty
	// path; regular files reuse the product's templates and carry the chunk
	// path.
	if task.Item.IsDependency {
		return p.links.dependency(ctx, galaxyPath)
	}

	res, err := p.links.product(ctx, task.Item.ProductID)
	if err != nil {
		return "", err
	}
	if !res.owned || len(res.templates) == 0 {
		// The plan drops unowned products, so this is a boundary guard rather
		// than an expected state: it keeps an empty template list from
		// becoming an index panic.
		return "", fmt.Errorf("galaxy: product %s has no usable cdn template", task.Item.ProductID)
	}
	return strings.ReplaceAll(res.templates[0], galaxy.GalaxyPathPlaceholder, "/"+galaxyPath), nil
}
