package transfer

import (
	"context"

	"github.com/nekrozis/goggo/internal/model"
)

// URLProvider resolves the URL of one chunk of one task.
//
// This is the seam that keeps transfer from depending on galaxy: the
// implementation lives in core, and transfer only ever asks where the chunk
// lives. Run's worker goroutines call URL concurrently, so an implementation must
// be safe for concurrent use.
type URLProvider interface {
	URL(ctx context.Context, task model.FileTask, chunk model.GalaxyDepotItemChunk) (string, error)
}
