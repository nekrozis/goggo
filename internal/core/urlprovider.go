package core

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/nekrozis/goggo/internal/galaxy"
	"github.com/nekrozis/goggo/internal/model"
)

// chunkURLProvider resolves the URL of one chunk through the Galaxy CDN
// machinery: hashToGalaxyPath -> secure/dependency link -> CDN template ->
// placeholder substitution, refreshing the Galaxy token before a chunk when it
// has expired (downloader.cpp:4642-4667).
//
// Workers call URL concurrently, so the per-product template cache and the
// token refresh are mutex-guarded. Dependency chunks re-resolve on every call
// and never touch the cache, exactly as upstream's per-worker copy does
// (downloader.cpp:4632-4640).
type chunkURLProvider struct {
	galaxy   *galaxy.Client
	priority []string
	refresh  func(context.Context) error
	expired  func() bool

	refreshMu   sync.Mutex
	mu          sync.Mutex
	prevProduct string
	templates   []string
}

// URL implements transfer.URLProvider.
func (p *chunkURLProvider) URL(ctx context.Context, task model.FileTask, chunk model.GalaxyDepotItemChunk) (string, error) {
	if p.expired() {
		if err := p.refreshIfExpired(ctx); err != nil {
			return "", fmt.Errorf("galaxy: refresh login: %w", err)
		}
	}

	galaxyPath := galaxy.HashToGalaxyPath(chunk.CompressedMD5)

	// Dependencies re-resolve the link for every chunk and substitute an empty
	// path; regular files reuse the product's templates and carry the chunk
	// path (downloader.cpp:4632-4667).
	if task.Item.IsDependency {
		doc, err := p.galaxy.DependencyLink(ctx, galaxyPath)
		if err != nil {
			return "", err
		}
		templates, err := galaxy.CdnURLTemplatesFromJSON(doc, p.priority)
		if err != nil {
			return "", err
		}
		if len(templates) == 0 {
			return "", fmt.Errorf("galaxy: dependency link returned no cdn url for %s", galaxyPath)
		}
		return strings.ReplaceAll(templates[0], galaxy.GalaxyPathPlaceholder, ""), nil
	}

	templates, err := p.templatesFor(ctx, task.Item.ProductID)
	if err != nil {
		return "", err
	}
	return strings.ReplaceAll(templates[0], galaxy.GalaxyPathPlaceholder, "/"+galaxyPath), nil
}

// refreshIfExpired refreshes the credentials when they have expired. The
// expiry check runs twice: once unlocked to skip the lock on the hot path, and
// again inside it, because the wait may have covered another worker's refresh —
// re-refreshing after that would issue a duplicate (and with a rotating refresh
// token, possibly a failing) request.
func (p *chunkURLProvider) refreshIfExpired(ctx context.Context) error {
	p.refreshMu.Lock()
	defer p.refreshMu.Unlock()
	if !p.expired() {
		return nil
	}
	return p.refresh(ctx)
}

// templatesFor caches the CDN url templates per product: regular files reuse
// them until the product changes (downloader.cpp:4633-4640).
func (p *chunkURLProvider) templatesFor(ctx context.Context, productID string) ([]string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if productID == p.prevProduct && len(p.templates) > 0 {
		return p.templates, nil
	}
	doc, err := p.galaxy.SecureLink(ctx, productID, "/")
	if err != nil {
		return nil, err
	}
	templates, err := galaxy.CdnURLTemplatesFromJSON(doc, p.priority)
	if err != nil {
		return nil, err
	}
	if len(templates) == 0 {
		return nil, fmt.Errorf("galaxy: secure link returned no cdn url for product %s", productID)
	}
	p.prevProduct = productID
	p.templates = templates
	return templates, nil
}
