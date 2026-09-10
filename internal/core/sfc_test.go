package core

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nekrozis/goggo/internal/model"
)

// sfcFixture builds a plan with one container (24 bytes) and two members cut
// from it, over an install root that already holds the container file.
type sfcFixture struct {
	root      string
	container string
	body      []byte
	res       PlanResult
}

func newSFCFixture(t *testing.T) *sfcFixture {
	t.Helper()
	f := &sfcFixture{root: t.TempDir()}
	f.container = f.root + "/galaxy_smallfilescontainer_42" // the plan stores forward-slash paths
	f.body = []byte("AAAABBBBCCCCDDDDYYYYZZZZ")             // members cut from here
	if err := os.WriteFile(f.container, f.body, 0o644); err != nil {
		t.Fatal(err)
	}
	f.res = PlanResult{
		InstallPath: f.root,
		Plan: model.DownloadPlan{
			SFC: []model.SFCGroup{{
				Container: model.GalaxyDepotItem{Path: "galaxy_smallfilescontainer_42", ProductID: "42"},
				Items: []model.GalaxyDepotItem{
					{Path: "game/one.txt", ProductID: "42", SFCOffset: 0, SFCSize: 8},
					{Path: "game/sub/two.txt", ProductID: "42", SFCOffset: 16, SFCSize: 8},
					// A member of another product: the extraction skips it,
					// the way the product_id filter does upstream.
					{Path: "game/foreign.txt", ProductID: "99", SFCOffset: 0, SFCSize: 4},
				},
			}},
		},
	}
	return f
}

// TestExtractSmallFilesContainers locks the unpacking: members are cut from
// the container by offset and size, written fresh, and the container is
// removed. A member of another product is not cut from this container.
func TestExtractSmallFilesContainers(t *testing.T) {
	f := newSFCFixture(t)
	d := newOfflineDownloader(t, noopServer(t), planTestConfig(t), newFakeConsole())

	if err := d.ExtractSmallFilesContainers(context.Background(), f.res); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	assertFileContent(t, filepath.Join(f.root, "game", "one.txt"), "AAAABBBB")
	assertFileContent(t, filepath.Join(f.root, "game", "sub", "two.txt"), "YYYYZZZZ")
	if _, err := os.Stat(filepath.Join(f.root, "game", "foreign.txt")); err == nil {
		t.Error("foreign.txt was written from the wrong product's container")
	}
	assertFileAbsent(t, f.container)

	out := consoleText(t, d)
	for _, want := range []string{
		"Extracting small files container " + f.container,
		"Deleting small files container " + f.container,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q", want)
		}
	}
}

// TestExtractSkipsMissingContainer locks the exists check: a container that is
// not on disk passes over silently.
func TestExtractSkipsMissingContainer(t *testing.T) {
	f := newSFCFixture(t)
	if err := os.Remove(f.container); err != nil {
		t.Fatal(err)
	}
	d := newOfflineDownloader(t, noopServer(t), planTestConfig(t), newFakeConsole())

	if err := d.ExtractSmallFilesContainers(context.Background(), f.res); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if strings.Contains(consoleText(t, d), "Extracting small files container") {
		t.Error("a missing container must be skipped without the extraction line")
	}
}

// noopServer is a server the extraction never talks to.
func noopServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
}

// consoleText pulls the output the fake console captured.
func consoleText(t *testing.T, d *Downloader) string {
	t.Helper()
	c, ok := d.ui.(*fakeConsole)
	if !ok {
		t.Fatalf("ui is %T, want *fakeConsole", d.ui)
	}
	return c.out.String()
}
