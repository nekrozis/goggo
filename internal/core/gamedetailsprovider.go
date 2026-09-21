package core

import (
	"context"
	"errors"
	"fmt"

	"github.com/nekrozis/goggo/internal/galaxy"
	"github.com/nekrozis/goggo/internal/gamedetails"
)

// The three ways a downlink document can fail to name a source. They are this
// package's own errors, not the transfer singlets the website path uses: the
// conversion layer skips a file whose resolver returned any error at all
// (gamedetails.DownlinkResolver), so nothing has to travel across that seam —
// these exist for the operator reading a diagnostic.
var (
	errEmptyDownlinkDoc  = errors.New("galaxy: empty downlink document")
	errNoDownlink        = errors.New("galaxy: downlink document has no downlink")
	errDownlinkNotString = errors.New("galaxy: downlink is not a string")
)

// gamedetailsResolver is a DownlinkResolver over the Galaxy API: one file entry
// names a downlink JSON document, and the document names the url the file is
// actually fetched from.
//
// It lives here because it needs all three of the API client, the url-to-path
// helper and the conversion's result type, while internal/gamedetails must not
// depend on transport.
type gamedetailsResolver struct {
	galaxy  *galaxy.Client
	refresh tokenRefresher
}

// The conversion asks for a resolver function, so the method itself is what has
// to satisfy it.
var _ gamedetails.DownlinkResolver = (*gamedetailsResolver)(nil).Resolve

// gamedetailsResolver builds the resolver this run's conversions use. Its shape
// follows chunkURLProvider: the package's own credentials and the refresh a
// request needs when they have expired.
func (d *Downloader) gamedetailsResolver() *gamedetailsResolver {
	return &gamedetailsResolver{
		galaxy:  d.galaxy,
		refresh: tokenRefresher{refresh: d.refreshAndSave, expired: func() bool { return d.token.Expired() }},
	}
}

// Resolve implements gamedetails.DownlinkResolver.
//
// The path is derived by galaxy.PathFromDownlinkURL and never reimplemented
// here, so the one url-to-path rule has one implementation.
func (r *gamedetailsResolver) Resolve(ctx context.Context, gamename, downlinkURL string) (gamedetails.ResolvedFile, error) {
	if err := r.refresh.refreshIfExpired(ctx); err != nil {
		return gamedetails.ResolvedFile{}, fmt.Errorf("galaxy: refresh login: %w", err)
	}

	doc, err := r.galaxy.ResponseJSON(ctx, downlinkURL)
	if err != nil {
		return gamedetails.ResolvedFile{}, err
	}
	if len(doc) == 0 {
		return gamedetails.ResolvedFile{}, errEmptyDownlinkDoc
	}
	raw, ok := doc["downlink"]
	if !ok || raw == nil {
		return gamedetails.ResolvedFile{}, errNoDownlink
	}
	// Shape before value: scalar string coercion must not decide what this
	// field is; downlink must strictly be a string.
	downlink, ok := raw.(string)
	if !ok {
		return gamedetails.ResolvedFile{}, fmt.Errorf("%w: got %s", errDownlinkNotString, mapKind(raw))
	}
	return gamedetails.ResolvedFile{
		URL:  downlink,
		Path: galaxy.PathFromDownlinkURL(downlink, gamename),
	}, nil
}
