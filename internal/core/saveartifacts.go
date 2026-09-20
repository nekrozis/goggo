package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/nekrozis/goggo/internal/gamedetails"
	"github.com/nekrozis/goggo/internal/transfer"
)

// This file is the save-* write side: the artifacts of one download run. The
// three writer contracts are separate functions with separate semantics, never
// one generic write-if-needed:
//
//	serials existing file ⇒ skip (never overwritten)
//	changelog equal content ⇒ skip; different ⇒ overwrite
//	json existing file ⇒ overwrite, unconditional
//	logo/icon re-downloaded every run through the shared attempt loop
//
// Every outcome lands in a SavedArtifact; the CLI renders the text, and only
// ArtifactFailed moves the exit code. A per-item failure continues the run
// rather than hiding itself.

// ArtifactKind names the six save-* artifacts.
type ArtifactKind uint8

const (
	ArtifactSerials ArtifactKind = iota
	ArtifactLogo
	ArtifactIcon
	ArtifactChangelog
	ArtifactGameDetailsJSON
	ArtifactProductJSON
)

// String names the kind for diagnostics.
func (k ArtifactKind) String() string {
	switch k {
	case ArtifactSerials:
		return "serials"
	case ArtifactLogo:
		return "logo"
	case ArtifactIcon:
		return "icon"
	case ArtifactChangelog:
		return "changelog"
	case ArtifactGameDetailsJSON:
		return "game-details-json"
	case ArtifactProductJSON:
		return "product-json"
	}
	return "artifact"
}

// ArtifactAction is the state machine of one artifact attempt.
type ArtifactAction uint8

const (
	// ArtifactWrote: the file now holds what this run wrote.
	ArtifactWrote ArtifactAction = iota
	// ArtifactSkippedExists: serials found a file already there.
	ArtifactSkippedExists
	// ArtifactSkippedUnchanged: the changelog content matched byte for byte.
	ArtifactSkippedUnchanged
	// ArtifactSkippedFormat: the <span> fail-closed — a warning;
	// it does NOT move the exit code.
	ArtifactSkippedFormat
	// ArtifactFailed: the write or download failed; Err is set; the command
	// aggregates to an operational failure while everything else continues.
	ArtifactFailed
)

// SavedArtifact is one artifact's structured verdict. Path is the destination
// the contract was evaluated against; Err carries the reason for the two
// non-clean states.
type SavedArtifact struct {
	Kind     ArtifactKind
	Gamename string
	Path     string
	Action   ArtifactAction
	Err      error
}

// saveGameArtifacts runs the save section for one product: the base game's
// artifacts, then each DLC's. game-details.json is decided by its path:
// MakeFilepaths gives it to the base game only, so a DLC simply has no path to
// write to.
func (d *Downloader) saveGameArtifacts(ctx context.Context, gd *gamedetails.GameDetails) []SavedArtifact {
	artifacts := d.saveArtifactsFor(ctx, gd)
	for i := range gd.DLCs {
		artifacts = append(artifacts, d.saveArtifactsFor(ctx, &gd.DLCs[i])...)
	}
	return artifacts
}

// saveArtifactsFor evaluates one product level's six artifacts under the gate
// "flag AND non-empty field". An empty field with the flag on records WHY when
// there is a reason to tell (fail-closed format, swallowed fetch failure) and
// stays silent otherwise: there is nothing to save.
func (d *Downloader) saveArtifactsFor(ctx context.Context, gd *gamedetails.GameDetails) []SavedArtifact {
	cfg := d.cfg.DownloadConfig
	var out []SavedArtifact

	text := func(flag bool, kind ArtifactKind, field, path string, write func(path, content, gamename string) SavedArtifact) {
		if !flag || path == "" {
			return
		}
		if field != "" {
			out = append(out, write(path, field, gd.Gamename))
			return
		}
		if kind == ArtifactSerials && gd.SerialsDiag != "" {
			out = append(out, SavedArtifact{Kind: kind, Gamename: gd.Gamename, Path: path,
				Action: ArtifactSkippedFormat, Err: errors.New(gd.SerialsDiag)})
			return
		}
		if gd.MetadataDiag != "" {
			out = append(out, SavedArtifact{Kind: kind, Gamename: gd.Gamename, Path: path,
				Action: ArtifactFailed, Err: errors.New(gd.MetadataDiag)})
		}
	}

	text(cfg.SaveSerials, ArtifactSerials, gd.Serials, gd.SerialsFilepath, writeSerials)
	if cfg.SaveLogo && gd.Logo != "" {
		out = append(out, d.downloadArtifact(ctx, gd.Logo, gd.LogoFilepath, ArtifactLogo, gd.Gamename))
	}
	if cfg.SaveIcon && gd.Icon != "" {
		out = append(out, d.downloadArtifact(ctx, gd.Icon, gd.IconFilepath, ArtifactIcon, gd.Gamename))
	}
	text(cfg.SaveChangelogs, ArtifactChangelog, gd.Changelog, gd.ChangelogFilepath, writeChangelog)
	text(cfg.SaveGameDetailsJSON, ArtifactGameDetailsJSON, gd.GameDetailsJson, gd.GameDetailsJSONFilepath,
		func(path, content, gamename string) SavedArtifact {
			return writeJSONFile(path, content, ArtifactGameDetailsJSON, gamename)
		})
	text(cfg.SaveProductJSON, ArtifactProductJSON, gd.ProductJson, gd.ProductJsonFilepath,
		func(path, content, gamename string) SavedArtifact {
			return writeJSONFile(path, content, ArtifactProductJSON, gamename)
		})
	return out
}

// writeSerials is contract one: an existing file is NEVER overwritten.
func writeSerials(path, content, gamename string) SavedArtifact {
	a := SavedArtifact{Kind: ArtifactSerials, Gamename: gamename, Path: path}
	if _, err := os.Stat(path); err == nil {
		a.Action = ArtifactSkippedExists
		return a
	}
	a.Action, a.Err = writeOrFail(path, content)
	return a
}

// writeChangelog is contract two: skip only when the existing content is
// byte-equal, otherwise overwrite.
func writeChangelog(path, content, gamename string) SavedArtifact {
	a := SavedArtifact{Kind: ArtifactChangelog, Gamename: gamename, Path: path}
	if existing, err := os.ReadFile(path); err == nil && string(existing) == content {
		a.Action = ArtifactSkippedUnchanged
		return a
	}
	a.Action, a.Err = writeOrFail(path, content)
	return a
}

// writeJSONFile is contract three: unconditional overwrite.
func writeJSONFile(path, content string, kind ArtifactKind, gamename string) SavedArtifact {
	a := SavedArtifact{Kind: kind, Gamename: gamename, Path: path}
	a.Action, a.Err = writeOrFail(path, content)
	return a
}

// writeOrFail prepares the parent directory and writes, mapping the three
// failure messages ("is not directory", "Failed to create directory", "Failed
// to create file") onto the failed action.
func writeOrFail(path, content string) (ArtifactAction, error) {
	if err := prepareArtifactDir(path); err != nil {
		return ArtifactFailed, err
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return ArtifactFailed, fmt.Errorf("Failed to create file: %s: %w", path, err)
	}
	return ArtifactWrote, nil
}

// prepareArtifactDir prepares the parent directory: an existing parent must BE
// a directory, a missing one is created.
func prepareArtifactDir(path string) error {
	dir := filepath.Dir(path)
	if fi, err := os.Stat(dir); err == nil {
		if !fi.IsDir() {
			return fmt.Errorf("%s is not directory", dir)
		}
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("Failed to create directory: %s: %w", dir, err)
	}
	return nil
}

// downloadArtifact sends one image URL through the shared attempt loop
// (transfer.DownloadArtifact). There is no exists-skip: the artifact is
// re-downloaded every run. The directory is prepared first because the attempt
// opens the destination directly.
func (d *Downloader) downloadArtifact(ctx context.Context, url, path string, kind ArtifactKind, gamename string) SavedArtifact {
	a := SavedArtifact{Kind: kind, Gamename: gamename, Path: path}
	if path == "" {
		a.Action, a.Err = ArtifactFailed, errors.New("no destination path for the artifact")
		return a
	}
	if err := prepareArtifactDir(path); err != nil {
		a.Action, a.Err = ArtifactFailed, err
		return a
	}
	if err := transfer.DownloadArtifact(ctx, url, path, d.transferOptions(), transfer.ArtifactDeps{HTTP: d.http}); err != nil {
		a.Action, a.Err = ArtifactFailed, err
		return a
	}
	a.Action = ArtifactWrote
	return a
}
