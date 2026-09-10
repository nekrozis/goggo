package transfer

import (
	"context"

	"github.com/nekrozis/goggo/internal/model"
)

// URLProvider resolves the URL of one chunk of one task.
//
// This is the seam that keeps transfer from depending on galaxy: the Galaxy
// implementation (core) does hashToGalaxyPath -> secure/dependency link -> CDN
// template -> URL, with per-product template caching for regular files and a
// per-chunk re-fetch for dependencies (downloader.cpp:4626-4667). That logic
// lives in core, not here — transfer only ever asks "where does this chunk
// live".
//
// Concurrency: Run's worker goroutines call URL concurrently, so an
// implementation must be safe for concurrent use. The Observer, by contrast,
// only ever sees events from transfer's single deliverer goroutine.
type URLProvider interface {
	URL(ctx context.Context, task model.FileTask, chunk model.GalaxyDepotItemChunk) (string, error)
}
