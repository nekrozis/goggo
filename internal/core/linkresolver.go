package core

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/nekrozis/goggo/internal/galaxy"
	"github.com/nekrozis/goggo/internal/httpx"
)

// linkResult is the answer for one product: whether the account owns it, and
// the CDN url templates its secure link carries. A product the account does
// not own has owned=false and no templates.
type linkResult struct {
	templates []string
	owned     bool
}

// linkResolver is one run's Galaxy link resolution. It answers, per product,
// whether the account owns it and — when it does — caches the CDN url
// templates the transfer builds chunk urls from.
//
// secure_link is the entitlement oracle the API offers, and it is the request
// the transfer would make anyway. The Downloader owns one resolver for the whole
// run, so the plan's probe, the old-build diff and the transfer share one request
// per product.
//
// A settled answer is cached: 403 means the account does not own the product, a
// 200 carries its templates. A transient failure is not an entitlement answer, so
// it is returned without being cached.
type linkResolver struct {
	galaxy   *galaxy.Client
	refresh  func(context.Context) error
	expired  func() bool
	settled  map[string]linkResult
	inflight map[string]*linkCall
	priority []string

	refreshMu sync.Mutex
	mu        sync.Mutex
}

// linkCall is one in-flight product resolution the other callers wait on, so a
// product is probed once even when many transfer workers ask for it at once.
// The fields are written before done is closed and read only after receiving
// from it.
type linkCall struct {
	done      chan struct{}
	err       error
	templates []string
	owned     bool
}

// newLinkResolver assembles the run's resolver from the downloader's clients.
func (d *Downloader) newLinkResolver() *linkResolver {
	return &linkResolver{
		galaxy:   d.galaxy,
		priority: d.cfg.DownloadConfig.GalaxyCDNPriority,
		refresh:  d.refreshAndSave,
		// An empty store has nothing to refresh: the session gate has already
		// rejected the commands that need one, and attempting a refresh with
		// no refresh token only replaces the answer with a local error.
		expired: func() bool { return d.token.Expired() && !d.token.Empty() },
	}
}

// product resolves one product's link, returning the entitlement answer and
// the CDN templates. See the type comment for the caching rule.
//
// Concurrent callers for the same product coalesce onto one request; callers
// for different products run in parallel, so a wide transfer does not
// serialise on the resolver.
func (l *linkResolver) product(ctx context.Context, productID string) (linkResult, error) {
	if err := l.refreshIfExpired(ctx); err != nil {
		return linkResult{}, fmt.Errorf("galaxy: refresh login: %w", err)
	}

	l.mu.Lock()
	if res, ok := l.settled[productID]; ok {
		l.mu.Unlock()
		return res, nil
	}
	if c, ok := l.inflight[productID]; ok {
		l.mu.Unlock()
		<-c.done
		return linkResult{owned: c.owned, templates: c.templates}, c.err
	}
	c := &linkCall{done: make(chan struct{})}
	if l.inflight == nil {
		l.inflight = make(map[string]*linkCall)
	}
	l.inflight[productID] = c
	l.mu.Unlock()

	res, err := l.fetch(ctx, productID)

	l.mu.Lock()
	delete(l.inflight, productID)
	if err == nil {
		if l.settled == nil {
			l.settled = make(map[string]linkResult)
		}
		l.settled[productID] = res
	}
	l.mu.Unlock()

	c.owned, c.templates, c.err = res.owned, res.templates, err
	close(c.done)
	return res, err
}

// fetch issues the single secure_link request behind product.
func (l *linkResolver) fetch(ctx context.Context, productID string) (linkResult, error) {
	doc, err := l.galaxy.SecureLink(ctx, productID, "/")
	if err != nil {
		if code, ok := statusCode(err); ok && code == http.StatusForbidden {
			// 403 is the entitlement answer, not a failure.
			return linkResult{owned: false}, nil
		}
		return linkResult{}, err
	}
	templates, err := galaxy.CdnURLTemplatesFromJSON(doc, l.priority)
	if err != nil {
		return linkResult{}, err
	}
	if len(templates) == 0 {
		return linkResult{}, fmt.Errorf("galaxy: secure link returned no cdn url for product %s", productID)
	}
	return linkResult{owned: true, templates: templates}, nil
}

// dependency resolves the CDN url of one dependency chunk. Dependencies are
// addressed by their galaxy path through a separate endpoint and are not
// cached: each chunk resolves its own link, which is what the endpoint is for.
func (l *linkResolver) dependency(ctx context.Context, galaxyPath string) (string, error) {
	if err := l.refreshIfExpired(ctx); err != nil {
		return "", fmt.Errorf("galaxy: refresh login: %w", err)
	}
	doc, err := l.galaxy.DependencyLink(ctx, galaxyPath)
	if err != nil {
		return "", err
	}
	templates, err := galaxy.CdnURLTemplatesFromJSON(doc, l.priority)
	if err != nil {
		return "", err
	}
	if len(templates) == 0 {
		return "", fmt.Errorf("galaxy: dependency link returned no cdn url for %s", galaxyPath)
	}
	return strings.ReplaceAll(templates[0], galaxy.GalaxyPathPlaceholder, ""), nil
}

// refreshIfExpired refreshes the credentials when they have expired. The
// expiry check runs twice: once unlocked to skip the lock on the hot path, and
// again inside it, because the wait may have covered another worker's refresh —
// re-refreshing after that would issue a duplicate (and with a rotating refresh
// token, possibly a failing) request. It is the run's only refresh, so the plan
// probe and the transfer share the guard.
func (l *linkResolver) refreshIfExpired(ctx context.Context) error {
	if !l.expired() {
		return nil
	}
	l.refreshMu.Lock()
	defer l.refreshMu.Unlock()
	if !l.expired() {
		return nil
	}
	return l.refresh(ctx)
}

// statusCode reports the HTTP status behind err, when it carries one.
func statusCode(err error) (int, bool) {
	if se, ok := errors.AsType[*httpx.StatusError](err); ok {
		return se.Code, true
	}
	return 0, false
}
