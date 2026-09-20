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
// completed is not issued twice. The unlocked check first keeps the lock off
// the hot path.
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

// checksumPolicy decides when the provider reads a downlink document's
// "checksum" url. The two chains have DIFFERENT gates and must not borrow each
// other's:
//
//   - checksumGated is the batch worker's rule — installers and patches only,
//     and only with remote XML enabled. Extras carry a checksum url on the real
//     API and are still not read.
//   - checksumAlways is the single-file rule — any matched file whose document
//     carries a non-empty checksum url is read, with no type gate and no
//     remote-XML gate. A failed or empty fetch is not a failure: the download
//     continues with no document.
type checksumPolicy int

const (
	checksumGated checksumPolicy = iota
	checksumAlways
)

// websiteURLProvider resolves a website file's download url through the Galaxy
// API: the downlink JSON document carries the "downlink" url and, for
// installers and patches, a "checksum" url whose document holds the md5 the
// version check compares against.
type websiteURLProvider struct {
	galaxy    *galaxy.Client
	remoteXML bool
	policy    checksumPolicy
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

	// The checksum document is read per the chain's policy: the batch gate is
	// type and configuration, the single-file gate is presence alone.
	checksumXML := ""
	readChecksum := p.policy == checksumAlways || (p.remoteXML && task.Checksummed)
	if readChecksum {
		if raw, ok := doc["checksum"]; ok && raw != nil {
			checksumURL, err := jsonval.Str(raw)
			if err != nil {
				if p.policy == checksumAlways {
					// The single-file chain never fails on the checksum
					// document; it downloads without it.
					return downlink, "", nil
				}
				return "", "", fmt.Errorf("checksum: %w", err)
			}
			if checksumURL != "" {
				checksumXML, err = p.galaxy.Response(ctx, checksumURL)
				if err != nil {
					if p.policy == checksumAlways {
						return downlink, "", nil
					}
					return "", "", err
				}
			}
		}
	}
	return downlink, checksumXML, nil
}
