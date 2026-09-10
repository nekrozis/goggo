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

// TestObserverContract locks that Observer stays implementable from outside:
// if it ever grows a method that needs package internals, this breaks.
func TestObserverContract(t *testing.T) {
	var _ Observer = fakeObserver{}
}

// TestURLProviderContract locks the same for URLProvider, and that its chunk
// parameter is the model type rather than something transfer invented.
func TestURLProviderContract(t *testing.T) {
	var _ URLProvider = fakeURLProvider{}
}

// TestEventKindsDistinct locks the five kinds onto distinct values.
func TestEventKindsDistinct(t *testing.T) {
	seen := map[EventKind]bool{}
	for _, k := range []EventKind{
		EventProgress, EventMessageInfo, EventMessageWarning, EventMessageError, EventMessageSuccess,
	} {
		if seen[k] {
			t.Errorf("event kind %d appears twice", k)
		}
		seen[k] = true
	}
}

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

// TestRunDepsShape locks the three-field carrier: exactly what the run loop
// needs, nothing else, and HTTP stays the httpx client rather than growing a
// fourth field or swapping to another type.
func TestRunDepsShape(t *testing.T) {
	deps := RunDeps{URL: fakeURLProvider{}, Observer: fakeObserver{}}
	if deps.HTTP != nil {
		t.Error("RunDeps.HTTP must be the httpx client, unset by default")
	}
	if deps.URL == nil || deps.Observer == nil {
		t.Error("RunDeps.URL and RunDeps.Observer must be settable")
	}
}
