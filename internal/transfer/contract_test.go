package transfer

import (
	"context"
	"testing"

	"github.com/nekrozis/goggo/internal/model"
)

// fakeObserver and fakeURLProvider are compile-time contract checks, not
// executable logic: they exist so the interfaces stay implementable from
// outside this package. Neither carries behaviour worth asserting beyond
// satisfying the interface.
type fakeObserver struct{}

func (fakeObserver) OnEvent(Event) {}

type fakeURLProvider struct{}

func (fakeURLProvider) URL(context.Context, model.FileTask, model.GalaxyDepotItemChunk) (string, error) {
	return "", nil
}

// The compile-time half of the contract: Observer and URLProvider stay
// implementable from outside this package, and URLProvider's chunk parameter
// stays the model type rather than something transfer invented. The fakes carry
// no behaviour, so this is a package-level assertion, not a test.
var (
	_ Observer    = fakeObserver{}
	_ URLProvider = fakeURLProvider{}
)

// TestEventShape locks that an event can express both halves of the contract: a
// progress point inside a chunk loop and a bare message.
func TestEventShape(t *testing.T) {
	progress := Event{
		Path: "/install/game/data.bin", Current: 1024, Total: 4096,
		ChunkIndex: 2, ChunkCount: 8, Kind: EventProgress,
	}
	if progress.Kind != EventProgress || progress.ChunkIndex != 2 || progress.ChunkCount != 8 {
		t.Errorf("progress event = %+v", progress)
	}
	if progress.Text != "" {
		t.Error("a progress event carries no text")
	}

	// File-level messages use -1: there is no chunk to point at.
	message := Event{Path: "/install/game/data.bin", Text: "File already exists", Kind: EventMessageInfo, ChunkIndex: -1}
	if message.ChunkIndex != -1 || message.Text == "" {
		t.Errorf("message event = %+v", message)
	}
}
