package cli

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/nekrozis/goggo/internal/core"
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
// Three properties matter, which is why this is not a bare Marshal: keys come out
// in byte order (Go's map marshalling), HTML escaping is OFF (the default would
// turn a URL's "&" and "<" into \u0026 and \u003c), and the indentation is a tab
// with a trailing newline. Numbers are re-serialised from Go's float64 values, so
// values beyond 2^53 are not reproduced exactly.
func renderManifest(w io.Writer, doc map[string]any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "\t")
	enc.SetEscapeHTML(false)
	return enc.Encode(doc)
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
