package cli

import (
	"fmt"
	"io"

	"github.com/nekrozis/goggo/internal/core"
	"github.com/nekrozis/goggo/internal/util"
)

// renderBuilds prints the build listing.
func renderBuilds(w io.Writer, rows []core.BuildRow) error {
	for _, row := range rows {
		if _, err := fmt.Fprintf(w, "%d: Version %s - %s (Gen %d) (Build id: %s)\n",
			row.Index, row.VersionName, row.DatePublished, row.Generation, row.BuildID); err != nil {
			return err
		}
	}
	return nil
}

// renderManifest prints the fetched manifest as styled JSON, through the writer
// every stored document goes through. See util.WriteStyledJSON for the style.
func renderManifest(w io.Writer, doc map[string]any) error {
	return util.WriteStyledJSON(w, doc)
}

// renderCDNNames prints one endpoint name per line.
func renderCDNNames(w io.Writer, names []string) error {
	for _, name := range names {
		if _, err := fmt.Fprintln(w, name); err != nil {
			return err
		}
	}
	return nil
}

// renderNotice prints one of the messages a command emits instead of doing the
// work, on the stream the notice carries. These are not failures: the run still
// ends with exit code 0.
func renderNotice(ui *console, n core.Notice) {
	if n.Text == "" {
		return
	}
	if n.Err {
		fmt.Fprintln(ui.ErrOut(), n.Text)
		return
	}
	fmt.Fprintln(ui.Out(), n.Text)
}

// renderNotices writes a whole message list in order. The read-only commands
// (verify, orphans) receive the plan's diagnostics as data and show them the way
// an install streams them: the error notices on the error stream, everything else
// on the output stream.
func renderNotices(ui *console, notices []core.Notice) {
	for _, notice := range notices {
		renderNotice(ui, notice)
	}
}
