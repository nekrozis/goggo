package core

import (
	"context"
	"fmt"
	"sync"

	"github.com/nekrozis/goggo/internal/galaxy"
	"github.com/nekrozis/goggo/internal/jsonval"
	"github.com/nekrozis/goggo/internal/model"
	"github.com/nekrozis/goggo/internal/transfer"
)

// tokenRefresher serialises credential refreshes across a run's workers and
// re-checks the expiry inside the lock, so a refresh another worker just
// completed is not issued twice (review round 2, S18d1). The unlocked check
// first keeps the lock off the hot path.
type tokenRefresher struct {
	mu      sync.Mutex
	refresh func(context.Context) error
	expired func() bool
}

// refreshIfExpired refreshes the credentials when they have expired.
func (t *tokenRefresher) refreshIfExpired(ctx context.Context) error {
	if !t.expired() {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.expired() {
		return nil
	}
	return t.refresh(ctx)
}

// websiteURLProvider resolves a website file's download url through the Galaxy
// API: the downlink JSON document carries the "downlink" url and, for
// installers and patches, a "checksum" url whose document holds the md5 the
// version check compares against (downloader.cpp:3094-3129).
type websiteURLProvider struct {
	galaxy    *galaxy.Client
	remoteXML bool
	refresh   tokenRefresher
}

// Resolve implements transfer.WebsiteURLProvider. Workers call it
// concurrently; the token refresh serialises itself, and the document fetches
// are independent requests.
func (p *websiteURLProvider) Resolve(ctx context.Context, task model.WebsiteTask) (string, string, error) {
	if err := p.refresh.refreshIfExpired(ctx); err != nil {
		return "", "", fmt.Errorf("galaxy: refresh login: %w", err)
	}

	doc, err := p.galaxy.ResponseJSON(ctx, task.DownlinkURL)
	if err != nil {
		return "", "", err
	}
	if len(doc) == 0 {
		return "", "", fmt.Errorf("galaxy: %w", transfer.ErrEmptyDownlink)
	}
	raw, ok := doc["downlink"]
	if !ok || raw == nil {
		return "", "", fmt.Errorf("galaxy: %w", transfer.ErrNoDownlink)
	}
	downlink, err := jsonval.Str(raw)
	if err != nil {
		return "", "", fmt.Errorf("downlink: %w", err)
	}

	// Only installers and patches consult the checksum document
	// (downloader.cpp:3110-3114).
	checksumXML := ""
	if p.remoteXML && task.Checksummed {
		if raw, ok := doc["checksum"]; ok && raw != nil {
			checksumURL, err := jsonval.Str(raw)
			if err != nil {
				return "", "", fmt.Errorf("checksum: %w", err)
			}
			if checksumURL != "" {
				checksumXML, err = p.galaxy.Response(ctx, checksumURL)
				if err != nil {
					return "", "", err
				}
			}
		}
	}
	return downlink, checksumXML, nil
}
