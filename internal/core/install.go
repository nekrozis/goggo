package core

import (
	"context"
	"errors"
	"fmt"
)

// ErrNotImplemented reports a command this build recognises but has not
// implemented yet. It is the orchestration-layer counterpart of the front end's
// "recognised but not implemented" answer: the same notice, raised where the
// command would have run instead of where the option is parsed.
var ErrNotImplemented = errors.New("not implemented in this build")

// Install runs a Galaxy install (Downloader::galaxyInstallGame,
// downloader.cpp:4022-4030).
//
// The engine it needs — the plan builder, the transfer layer and the download
// loop — is not ported yet, so this stops with ErrNotImplemented rather than
// reporting a success that never happened. The request describes everything up
// to the manifest, which is why that type exists before its consumer does.
func (d *Downloader) Install(ctx context.Context, req InstallRequest) error {
	return fmt.Errorf("galaxy install: %w", ErrNotImplemented)
}
