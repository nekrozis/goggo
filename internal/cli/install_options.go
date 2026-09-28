package cli

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/nekrozis/goggo/internal/config"
	"github.com/nekrozis/goggo/internal/core"
	"github.com/nekrozis/goggo/internal/util"
)

// renderInstallOptions prints the available installation options table and the
// DLCs the selected build carries.
func renderInstallOptions(w io.Writer, res core.InstallOptionsResult) error {
	if len(res.Entries) == 0 {
		fmt.Fprintf(w, "No compatible installation options found for %q (Build: %s)\n", res.GameTitle, res.BuildID)
	} else {
		fmt.Fprintf(w, "Available installation options for %q (Build: %s):\n\n", res.GameTitle, res.BuildID)
		if err := writeOptionsTable(w, res.Entries); err != nil {
			return err
		}
	}

	if len(res.DLCs) > 0 {
		fmt.Fprint(w, "\nDLCs (install some of them with --dlc <id|title>):\n\n")
		if err := writeDLCTable(w, res.DLCs); err != nil {
			return err
		}
	}
	return nil
}

// writeOptionsTable renders the (platform, arch, language) tuples.
func writeOptionsTable(w io.Writer, entries []core.InstallOptionEntry) error {
	tw := tabwriter.NewWriter(w, 0, 4, 4, ' ', 0)
	fmt.Fprintln(tw, "PLATFORM\tARCH\tLANGUAGE\tSIZE (EST)\tFILES")
	for _, entry := range entries {
		sizeStr := util.SizeString(uint64(entry.Size), config.UnitFormatIEC)
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\n",
			entry.Platform, entry.Arch, entry.Language, sizeStr, entry.Files)
	}
	return tw.Flush()
}

// writeDLCTable renders the DLC section: the product id --dlc accepts, the
// title, and whether the account owns it. Both identity columns are shown
// because either one may be the value the user has to hand.
func writeDLCTable(w io.Writer, dlcs []core.DLCOption) error {
	tw := tabwriter.NewWriter(w, 0, 4, 4, ' ', 0)
	fmt.Fprintln(tw, "PRODUCT ID\tTITLE\tOWNED")
	for _, dlc := range dlcs {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", dlc.ProductID, dlc.Title, ownedWord(dlc.Owned))
	}
	return tw.Flush()
}

// ownedWord is how the listing answers its ownership column.
func ownedWord(owned bool) string {
	if owned {
		return "yes"
	}
	return "no"
}
