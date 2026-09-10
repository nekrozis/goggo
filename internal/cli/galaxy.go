package cli

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/nekrozis/goggo/internal/core"
)

// renderBuilds prints the build listing (downloader.cpp:4906-4913).
func renderBuilds(w io.Writer, rows []core.BuildRow) error {
	for _, row := range rows {
		if _, err := fmt.Fprintf(w, "%d: Version %s - %s (Gen %d) (Build id: %s)\n",
			row.Index, row.VersionName, row.DatePublished, row.Generation, row.BuildID); err != nil {
			return err
		}
	}
	return nil
}

// renderManifest prints the fetched manifest the way the C++ source does
// (Json::StyledStreamWriter, downloader.cpp:4933).
//
// Three properties matter and are the reason this is not a bare Marshal:
//
//   - keys come out in byte order, which is what Go's map marshalling and
//     jsoncpp's std::map agree on;
//   - HTML escaping is OFF: the default would turn the "&" and "<" of a URL
//     into \u0026 and \u003c;
//   - the indentation is a tab and the document ends with a newline.
//
// Difference (recorded): the numeric text is produced by re-serialising Go's
// float64 values, so values beyond 2^53 cannot be reproduced exactly and
// jsoncpp's own precision rules are not reproduced. jsoncpp is an unpinned
// system dependency, so byte-for-byte equality is not a promise this port can
// make or check.
func renderManifest(w io.Writer, doc map[string]any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "\t")
	enc.SetEscapeHTML(false)
	return enc.Encode(doc)
}

// renderCDNNames prints one endpoint name per line (downloader.cpp:4394-4395).
func renderCDNNames(w io.Writer, names []string) error {
	for _, name := range names {
		if _, err := fmt.Fprintln(w, name); err != nil {
			return err
		}
	}
	return nil
}

// renderNotice prints one of the messages the C++ source emits instead of doing
// the work, on the stream it used. These are not failures: the run still ends
// with exit code 0 (main.cpp:842,897).
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
