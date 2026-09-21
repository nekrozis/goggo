package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/nekrozis/goggo/internal/blacklist"
	"github.com/nekrozis/goggo/internal/config"
	"github.com/nekrozis/goggo/internal/core"
	"github.com/nekrozis/goggo/internal/gamedetails"
	"github.com/nekrozis/goggo/internal/util"
)

// This file renders the two list leaves. The text renderer prints the download
// face of each game, down to the trailing spaces and the DLC vector order; the
// JSON renderer emits the same data as JSON through the one styled writer. Both
// are display-only: list never writes and never transfers.

// runListDetails acquires and renders one of the two detail formats. A
// blacklist failure is fatal for the text format (its file rows depend on
// the filter) and irrelevant for JSON (it filters nothing).
func runListDetails(ctx context.Context, d *core.Downloader, inv invocation, stdout, stderr io.Writer) outcome {
	games, err := d.ListGameDetails(ctx, inv.args, productRefMode(inv))
	if err != nil {
		return reportError(stderr, err)
	}

	bl, err := blacklist.LoadBlacklist(inv.cfg.BlacklistFilePath)
	if err != nil {
		return reportError(stderr, err)
	}
	renderGameDetailsText(stdout, stderr, games, bl, inv.cfg.MsgLevel >= msgLevelVerbose)
	return outcomeOK
}

// renderArtifacts turns the write side's ledger into lines. Serials finding an
// existing file prints NOTHING — the skip is recorded in the ledger, and the
// exit code is unaffected.
func renderArtifacts(out, errOut io.Writer, saved []core.SavedArtifact) {
	for _, a := range saved {
		switch a.Action {
		case core.ArtifactWrote:
			switch a.Kind {
			case core.ArtifactSerials:
				fmt.Fprintf(out, "Saving serials: %s\n", a.Path)
			case core.ArtifactChangelog:
				fmt.Fprintf(out, "Saving changelog: %s\n", a.Path)
			case core.ArtifactGameDetailsJSON, core.ArtifactProductJSON:
				fmt.Fprintf(out, "Saving JSON data: %s\n", a.Path)
			default:
				fmt.Fprintf(out, "Saving %s: %s\n", a.Kind, a.Path)
			}
		case core.ArtifactSkippedExists:
			// silent by design
		case core.ArtifactSkippedUnchanged:
			fmt.Fprintf(out, "Changelog unchanged. Skipping: %s\n", a.Path)
		case core.ArtifactSkippedFormat:
			fmt.Fprintf(errOut, "Warning: %v: %s\n", a.Err, a.Path)
		case core.ArtifactFailed:
			fmt.Fprintf(errOut, "Failed: %s: %v\n", a.Path, a.Err)
		}
	}
}

// renderGameDetailsText walks the games in the acquisition's gamename order.
func renderGameDetailsText(out, errOut io.Writer, games []gamedetails.GameDetails, bl *blacklist.Blacklist, verbose bool) {
	for i := range games {
		printGameDetailsText(out, errOut, &games[i], bl, verbose)
	}
}

func printGameDetailsText(out, errOut io.Writer, gd *gamedetails.GameDetails, bl *blacklist.Blacklist, verbose bool) {
	fmt.Fprintf(out, "gamename: %s\nproduct id: %s\ntitle: %s\nicon: %s\n", gd.Gamename, gd.ProductID, gd.Title, gd.Icon)
	if gd.Serials != "" {
		fmt.Fprintf(out, "serials:\n%s\n", gd.Serials)
	}
	// The base vector order, with the trailing-space headers.
	for _, v := range []struct {
		header string
		list   []gamedetails.GameFile
	}{
		{"installers: ", gd.Installers},
		{"extras: ", gd.Extras},
		{"patches: ", gd.Patches},
		{"language packs: ", gd.LanguagePacks},
	} {
		if len(v.list) == 0 {
			continue
		}
		fmt.Fprintln(out, v.header)
		for j := range v.list {
			printGameFileDetailsText(out, errOut, &v.list[j], bl, verbose)
		}
	}
	if len(gd.DLCs) > 0 {
		fmt.Fprintln(out, "DLCs: ")
		for i := range gd.DLCs {
			dlc := &gd.DLCs[i]
			fmt.Fprintf(out, "DLC gamename: %s\nproduct id: %s\n", dlc.Gamename, dlc.ProductID)
			if dlc.Serials != "" {
				// The DLC serials line has no newline after the label.
				fmt.Fprintf(out, "serials:%s\n", dlc.Serials)
			}
			// The DLC vector order: installers, patches, extras,
			// language packs — WITHOUT section headers.
			for _, list := range [][]gamedetails.GameFile{dlc.Installers, dlc.Patches, dlc.Extras, dlc.LanguagePacks} {
				for j := range list {
					printGameFileDetailsText(out, errOut, &list[j], bl, verbose)
				}
			}
		}
	}
}

func printGameFileDetailsText(out, errOut io.Writer, gf *gamedetails.GameFile, bl *blacklist.Blacklist, verbose bool) {
	path := gf.GetFilepath()
	if bl.IsBlacklisted(path) {
		if verbose {
			fmt.Fprintf(errOut, "skipped blacklisted file %s\n", path)
		}
		return
	}
	fmt.Fprintf(out, "\tid: %s\n\tname: %s\n\tpath: %s\n\tsize: %s\n", gf.ID, gf.Name, gf.Path, gf.Size)
	if gf.Type&config.GFInstaller != 0 {
		updated := "False"
		if gf.Updated != 0 {
			updated = "True"
		}
		fmt.Fprintf(out, "\tupdated: %s\n", updated)
	}
	if gf.Type&(config.GFInstaller|config.GFPatch) != 0 {
		fmt.Fprintf(out, "\tlanguage: %s\n", util.OptionNameString(gf.Language, config.Languages))
	}
	if gf.Type&config.GFInstaller != 0 {
		fmt.Fprintf(out, "\tversion: %s\n", gf.Version)
	}
	fmt.Fprintln(out)
}
