package transfer

import (
	"context"

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

// The -1 ChunkIndex convention for file-level events, and the "a progress
// event carries no text" rule, are properties of how CONSUMERS build events;
// the renderer and observer tests exercise them where they are used. A test
// here would only read back a struct literal.
