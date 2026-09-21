package cli

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/nekrozis/goggo/internal/config"
	"github.com/nekrozis/goggo/internal/core"
	"github.com/nekrozis/goggo/internal/util"
)

// renderInstallOptions prints the available installation options table.
func renderInstallOptions(w io.Writer, res core.InstallOptionsResult) error {
	if len(res.Entries) == 0 {
		fmt.Fprintf(w, "No compatible installation options found for %q (Build: %s)\n", res.GameTitle, res.BuildID)
		return nil
	}

	fmt.Fprintf(w, "Available installation options for %q (Build: %s):\n\n", res.GameTitle, res.BuildID)

	tw := tabwriter.NewWriter(w, 0, 4, 4, ' ', 0)
	fmt.Fprintln(tw, "PLATFORM\tARCH\tLANGUAGE\tSIZE (EST)\tFILES")
	for _, entry := range res.Entries {
		sizeStr := util.SizeString(uint64(entry.Size), config.UnitFormatIEC)
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\n",
			entry.Platform, entry.Arch, entry.Language, sizeStr, entry.Files)
	}
	return tw.Flush()
}
