package cli

import (
	"encoding/json/jsontext"
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

// renderManifest prints the fetched manifest as styled JSON.
//
// The styling is not spelled here: util.WriteStyledJSON is the project's single
// writer seam, and a second copy of the same encoder settings is how the two
// drift apart. Keys come out in byte order (Go's map marshalling), HTML escaping
// is off so a URL's "&" and "<" stay as themselves, and the indentation is a tab
// with a trailing newline. A member is raw JSON text, so a number is emitted as
// the literal the server sent rather than as a re-serialised float64.
func renderManifest(w io.Writer, doc map[string]jsontext.Value) error {
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
func renderNotice(stdout, stderr io.Writer, n core.Notice) {
	if n.Text == "" {
		return
	}
	if n.Err {
		fmt.Fprintln(stderr, n.Text)
		return
	}
	fmt.Fprintln(stdout, n.Text)
}

// renderNotices writes a whole message list in order. The read-only commands
// (verify, orphans) receive the plan's diagnostics as data and show them the way
// an install streams them: the error notices on the error stream, everything else
// on the output stream.
func renderNotices(stdout, stderr io.Writer, notices []core.Notice) {
	for _, notice := range notices {
		renderNotice(stdout, stderr, notice)
	}
}
