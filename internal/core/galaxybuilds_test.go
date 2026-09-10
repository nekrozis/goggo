package core

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/nekrozis/goggo/internal/config"
	"github.com/nekrozis/goggo/internal/galaxy"
)

// Fixture data for the two Galaxy commands. The hashes are bare (as a build link
// carries them) and their content-system paths are derived with
// galaxy.HashToGalaxyPath, so the test cannot agree with the implementation by
// accident: a wrong expansion makes the fixture answer 404.
const (
	fixtureProductID  = "123"
	fixtureHashTwo    = "2222222222222222222222222222222222222222"
	fixtureHashBranch = "3333333333333333333333333333333333333333"
	fixtureV1Path     = "/content-system/v1/manifests/123/windows/42/repository.json"
)

const (
	fixtureProductsOne  = `{"page":1,"totalPages":1,"products":[{"id":"555","slug":"Some Game"}]}`
	fixtureProductsTwo  = `{"page":1,"totalPages":1,"products":[{"id":"555","slug":"Some Game A"},{"id":"556","slug":"Some Game B"}]}`
	fixtureProductsNone = `{"page":1,"totalPages":1,"products":[]}`
)

// defaultBuildsBody is the build list the fixture serves: three builds whose
// document order differs from both sorting orders, so an implementation that
// forgets to sort cannot pass by accident.
func defaultBuildsBody() string {
	return `{"items":[` +
		`{"build_id":"b-branch","version_name":"1.1.0-beta","date_published":"2024-06-01",` +
		`"generation":2,"branch":"compatibility",` +
		`"link":"https://cdn.gog.com/content-system/v2/meta/dead/` + fixtureHashBranch + `"},` +
		`{"build_id":"b-old","version_name":"1.0.1","date_published":"2024-01-05","generation":1,` +
		`"link":"https://cdn.gog.com` + fixtureV1Path + `"},` +
		`{"build_id":"b-new","version_name":"1.0.2","date_published":"2024-03-02","generation":2,` +
		`"link":"https://cdn.gog.com/content-system/v2/meta/dead/` + fixtureHashTwo + `"}]}`
}

// fixtureServer serves the Galaxy and website responses the two commands read,
// and records the paths it was asked for.
type fixtureServer struct {
	*httptest.Server

	mu       sync.Mutex
	requests []string
	builds   string
	products string
}

func newFixtureServer(t *testing.T) *fixtureServer {
	t.Helper()
	f := &fixtureServer{builds: defaultBuildsBody(), products: fixtureProductsOne}
	v2Two := galaxy.HashToGalaxyPath(fixtureHashTwo)
	v2Branch := galaxy.HashToGalaxyPath(fixtureHashBranch)

	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.requests = append(f.requests, r.URL.Path)
		builds, products := f.builds, f.products
		f.mu.Unlock()

		switch {
		case strings.HasSuffix(r.URL.Path, "/builds"):
			fmt.Fprint(w, builds)
		case r.URL.Path == "/content-system/v2/meta/"+v2Two:
			fmt.Fprint(w, `{"depots":[{"manifest":"2222"}]}`)
		case r.URL.Path == "/content-system/v2/meta/"+v2Branch:
			fmt.Fprint(w, `{"depots":[{"manifest":"3333"}]}`)
		case r.URL.Path == fixtureV1Path:
			fmt.Fprint(w, `{"depots":[{"manifest":"1111"}]}`)
		case strings.HasSuffix(r.URL.Path, "/secure_link"):
			fmt.Fprint(w, `{"urls":[{"endpoint_name":"gog-cdn-fastly"},`+
				`{"endpoint_name":""},{"endpoint_name":"gog-cdn-cloudflare"}]}`)
		case r.URL.Path == "/www/user/data/games":
			fmt.Fprint(w, `{"owned":["555"]}`)
		case r.URL.Path == "/www/account/getFilteredProducts":
			fmt.Fprint(w, products)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fixtureServer) setBuilds(body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.builds = body
}

func (f *fixtureServer) setProducts(body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.products = body
}

// seen counts the recorded requests whose path contains want.
func (f *fixtureServer) seen(want string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	var n int
	for _, path := range f.requests {
		if strings.Contains(path, want) {
			n++
		}
	}
	return n
}

// galaxyTestConfig is the configuration the commands are driven with: the
// defaults plus the two Galaxy values, which the CLI normally supplies (S17
// keeps those defaults in internal/cli).
func galaxyTestConfig(t *testing.T, sortOrder string) config.Config {
	t.Helper()
	dir := t.TempDir()
	cfg := config.NewConfig(dir, dir)
	cfg.GalaxyBuildSortingOrder = sortOrder
	cfg.DownloadConfig.GalaxyPlatform = config.PlatformWindows
	return cfg
}

func TestShowBuildsListsBuildsWhenNoneSelected(t *testing.T) {
	// The default order ("score") sorts the list before it is printed, and the
	// branch build is pushed to the end (downloader.cpp:6860-6885).
	cases := []struct {
		name      string
		sortOrder string
		want      []BuildRow
	}{
		{
			name: "score pushes branch builds behind", sortOrder: "score",
			want: []BuildRow{
				{Index: 0, VersionName: "1.0.2", DatePublished: "2024-03-02", Generation: 2, BuildID: "b-new"},
				{Index: 1, VersionName: "1.0.1", DatePublished: "2024-01-05", Generation: 1, BuildID: "b-old"},
				{Index: 2, VersionName: "1.1.0-beta", DatePublished: "2024-06-01", Generation: 2, BuildID: "b-branch"},
			},
		},
		{
			name: "date is newest first", sortOrder: "date",
			want: []BuildRow{
				{Index: 0, VersionName: "1.1.0-beta", DatePublished: "2024-06-01", Generation: 2, BuildID: "b-branch"},
				{Index: 1, VersionName: "1.0.2", DatePublished: "2024-03-02", Generation: 2, BuildID: "b-new"},
				{Index: 2, VersionName: "1.0.1", DatePublished: "2024-01-05", Generation: 1, BuildID: "b-old"},
			},
		},
		{
			name: "none keeps the document order", sortOrder: "none",
			want: []BuildRow{
				{Index: 0, VersionName: "1.1.0-beta", DatePublished: "2024-06-01", Generation: 2, BuildID: "b-branch"},
				{Index: 1, VersionName: "1.0.1", DatePublished: "2024-01-05", Generation: 1, BuildID: "b-old"},
				{Index: 2, VersionName: "1.0.2", DatePublished: "2024-03-02", Generation: 2, BuildID: "b-new"},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := newFixtureServer(t)
			d := newOfflineDownloader(t, srv.Server, galaxyTestConfig(t, c.sortOrder), newFakeConsole())

			res, err := d.ShowBuilds(context.Background(), fixtureProductID, "")
			if err != nil {
				t.Fatalf("ShowBuilds: %v", err)
			}
			if res.Notice.Text != "" {
				t.Fatalf("notice = %q, want none", res.Notice.Text)
			}
			if res.Manifest != nil {
				t.Fatalf("manifest = %v, want none: no build was selected", res.Manifest)
			}
			if len(res.Builds) != len(c.want) {
				t.Fatalf("builds = %v, want %d rows", res.Builds, len(c.want))
			}
			for i, want := range c.want {
				if res.Builds[i] != want {
					t.Errorf("row %d = %+v, want %+v", i, res.Builds[i], want)
				}
			}
			if srv.seen("/os/windows/builds") != 1 {
				t.Errorf("the build list must be fetched once, got %d", srv.seen("/os/windows/builds"))
			}
		})
	}
}

// TestShowBuildsFetchesManifest covers both manifest generations: generation 2
// expands the last segment of the link, generation 1 fetches the link itself.
func TestShowBuildsFetchesManifest(t *testing.T) {
	cases := []struct {
		name     string
		buildID  string
		wantPath string
	}{
		{
			name: "generation 2 expands the build hash", buildID: "b-new",
			wantPath: "/content-system/v2/meta/" + galaxy.HashToGalaxyPath(fixtureHashTwo),
		},
		{
			name: "a numeric argument is an index", buildID: "1",
			// With the document order, index 1 is the generation 1 build.
			wantPath: fixtureV1Path,
		},
		{
			name: "generation 1 fetches the link as given", buildID: "b-old",
			wantPath: fixtureV1Path,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := newFixtureServer(t)
			d := newOfflineDownloader(t, srv.Server, galaxyTestConfig(t, "none"), newFakeConsole())

			res, err := d.ShowBuilds(context.Background(), fixtureProductID, c.buildID)
			if err != nil {
				t.Fatalf("ShowBuilds: %v", err)
			}
			if res.Manifest == nil {
				t.Fatalf("manifest = nil (notice %q), want the fetched document", res.Notice.Text)
			}
			if srv.seen(c.wantPath) != 1 {
				t.Errorf("requests = %v, want exactly one %s", srv.requests, c.wantPath)
			}
		})
	}
}

// TestShowBuildsUnsupportedGeneration covers the C++ switch default: only
// generation 1 and 2 are fetched, anything else prints the message and stops.
func TestShowBuildsUnsupportedGeneration(t *testing.T) {
	srv := newFixtureServer(t)
	srv.setBuilds(`{"items":[` +
		`{"build_id":"b-one","version_name":"1.0","date_published":"2024-01-01","generation":1,"link":""},` +
		`{"build_id":"b-three","version_name":"2.0","date_published":"2024-02-02","generation":3,"link":""}]}`)
	d := newOfflineDownloader(t, srv.Server, galaxyTestConfig(t, "none"), newFakeConsole())

	res, err := d.ShowBuilds(context.Background(), fixtureProductID, "1")
	if err != nil {
		t.Fatalf("ShowBuilds: %v", err)
	}
	if res.Notice.Text != msgGenerationsOneTwo {
		t.Errorf("notice = %q, want %q", res.Notice.Text, msgGenerationsOneTwo)
	}
	if res.Manifest != nil {
		t.Error("no manifest must be fetched for an unsupported generation")
	}
	if srv.seen("/content-system/") != 0 {
		t.Errorf("requests = %v, want no manifest request", srv.requests)
	}
}

// TestShowBuildsIndexOutOfRange covers an index past the end of the list: the
// C++ source reads a null entry, whose generation is 0, so the same message
// comes out.
func TestShowBuildsIndexOutOfRange(t *testing.T) {
	srv := newFixtureServer(t)
	d := newOfflineDownloader(t, srv.Server, galaxyTestConfig(t, "none"), newFakeConsole())

	res, err := d.ShowBuilds(context.Background(), fixtureProductID, "9")
	if err != nil {
		t.Fatalf("ShowBuilds: %v", err)
	}
	if res.Notice.Text != msgGenerationsOneTwo {
		t.Errorf("notice = %q, want %q", res.Notice.Text, msgGenerationsOneTwo)
	}
}

// TestShowBuildsLinuxWithoutBuilds covers the fallback branch: the two messages
// are printed and the run then reports that the installer fallback is not
// ported, instead of exiting as though it had run (review ruling D13).
func TestShowBuildsLinuxWithoutBuilds(t *testing.T) {
	srv := newFixtureServer(t)
	srv.setBuilds(`{}`)
	cfg := galaxyTestConfig(t, "score")
	cfg.DownloadConfig.GalaxyPlatform = config.PlatformLinux
	d := newOfflineDownloader(t, srv.Server, cfg, newFakeConsole())

	res, err := d.ShowBuilds(context.Background(), fixtureProductID, "")
	if !errors.Is(err, errInstallerFallback) {
		t.Fatalf("err = %v, want errInstallerFallback", err)
	}
	want := msgNoLinuxSupport + "\n" + msgCheckInstallers
	if res.Notice.Text != want {
		t.Errorf("notice = %q, want %q", res.Notice.Text, want)
	}
	if got := srv.seen("/os/linux/builds"); got != 1 {
		t.Errorf("linux build requests = %d, want 1: the path segment follows --galaxy-platform", got)
	}
}

func TestSortProductBuildsLeavesUnknownOrdersAlone(t *testing.T) {
	// The C++ source only writes the list back inside its two named branches,
	// so an unrecognised order is not a reordering.
	for _, order := range []string{"", "none", "whatever"} {
		srv := newFixtureServer(t)
		d := newOfflineDownloader(t, srv.Server, galaxyTestConfig(t, order), newFakeConsole())
		doc := map[string]any{"items": []any{
			map[string]any{"build_id": "a"}, map[string]any{"build_id": "b"},
		}}
		got, err := d.sortProductBuilds(doc)
		if err != nil {
			t.Fatalf("sortProductBuilds(%q): %v", order, err)
		}
		items, err := buildItems(got)
		if err != nil {
			t.Fatalf("buildItems: %v", err)
		}
		if len(items) != 2 || items[0].(map[string]any)["build_id"] != "a" {
			t.Errorf("order %q reordered the list: %v", order, items)
		}
	}
}

func TestSortProductBuildsShapeErrors(t *testing.T) {
	srv := newFixtureServer(t)
	d := newOfflineDownloader(t, srv.Server, galaxyTestConfig(t, "date"), newFakeConsole())

	// A missing or null list is no builds at all; a wrong shape is a protocol
	// error (the tier the depot step established).
	for _, doc := range []map[string]any{{}, {"items": nil}} {
		if _, err := d.sortProductBuilds(doc); err != nil {
			t.Errorf("doc %v must read as empty, got %v", doc, err)
		}
	}
	if _, err := d.sortProductBuilds(map[string]any{"items": map[string]any{}}); err == nil {
		t.Error("a non-array items value must be an error")
	}
}

func TestBuildIndexFor(t *testing.T) {
	items := []any{
		map[string]any{"build_id": "b-one"},
		map[string]any{"build_id": "b-two"},
	}
	cases := []struct {
		buildID string
		want    int
	}{
		{"b-one", 0},
		{"b-two", 1},
		{"1", 1},
		{"-1", -1},
		{"nonsense", -1},
		{"", -1},
		{"2x", -1}, // std::stoi would read 2; this port requires a whole integer
	}
	for _, c := range cases {
		got, err := buildIndexFor(items, c.buildID)
		if err != nil {
			t.Fatalf("buildIndexFor(%q): %v", c.buildID, err)
		}
		if got != c.want {
			t.Errorf("buildIndexFor(%q) = %d, want %d", c.buildID, got, c.want)
		}
	}

	if _, err := buildIndexFor([]any{"not an object"}, ""); err == nil {
		t.Error("a non-object entry must be an error")
	}
}

func TestListCDNs(t *testing.T) {
	srv := newFixtureServer(t)
	d := newOfflineDownloader(t, srv.Server, galaxyTestConfig(t, "none"), newFakeConsole())

	res, err := d.ListCDNs(context.Background(), fixtureProductID, "b-new")
	if err != nil {
		t.Fatalf("ListCDNs: %v", err)
	}
	if res.Notice.Text != "" {
		t.Fatalf("notice = %q, want none", res.Notice.Text)
	}
	want := []string{"gog-cdn-fastly", "gog-cdn-cloudflare"}
	if len(res.Names) != len(want) {
		t.Fatalf("names = %v, want %v (the empty endpoint name is skipped)", res.Names, want)
	}
	for i := range want {
		if res.Names[i] != want[i] {
			t.Errorf("name %d = %q, want %q", i, res.Names[i], want[i])
		}
	}
	if srv.seen("/secure_link") != 1 {
		t.Errorf("requests = %v, want one secure link call", srv.requests)
	}
}

func TestListCDNsRequiresGenerationTwo(t *testing.T) {
	srv := newFixtureServer(t)
	d := newOfflineDownloader(t, srv.Server, galaxyTestConfig(t, "none"), newFakeConsole())

	res, err := d.ListCDNs(context.Background(), fixtureProductID, "b-old")
	if err != nil {
		t.Fatalf("ListCDNs: %v", err)
	}
	if res.Notice.Text != msgGenerationTwoOnly {
		t.Errorf("notice = %q, want %q", res.Notice.Text, msgGenerationTwoOnly)
	}
	if srv.seen("/secure_link") != 0 {
		t.Errorf("requests = %v, want no secure link call", srv.requests)
	}
}

func TestListCDNsLinuxWithoutBuilds(t *testing.T) {
	srv := newFixtureServer(t)
	srv.setBuilds(`{}`)
	cfg := galaxyTestConfig(t, "score")
	cfg.DownloadConfig.GalaxyPlatform = config.PlatformLinux
	d := newOfflineDownloader(t, srv.Server, cfg, newFakeConsole())

	res, err := d.ListCDNs(context.Background(), fixtureProductID, "")
	if err != nil {
		t.Fatalf("ListCDNs: %v", err)
	}
	// One line here, unlike show-builds, which continues into the installer
	// fallback (downloader.cpp:4363 vs 4859-4861).
	if res.Notice.Text != msgNoLinuxSupport {
		t.Errorf("notice = %q, want %q", res.Notice.Text, msgNoLinuxSupport)
	}
}

// TestSelectProductIDNumeric covers the numeric shortcut: the argument IS the
// product id, so the account is never listed.
func TestSelectProductIDNumeric(t *testing.T) {
	srv := newFixtureServer(t)
	ui := newFakeConsole()
	d := newOfflineDownloader(t, srv.Server, galaxyTestConfig(t, "score"), ui)

	if _, err := d.ShowBuilds(context.Background(), "456", ""); err != nil {
		t.Fatalf("ShowBuilds: %v", err)
	}
	if got := srv.seen("/account/getFilteredProducts"); got != 0 {
		t.Errorf("the account was listed %d times, want none for a numeric id", got)
	}
	if got := srv.seen("/products/456/os/windows/builds"); got != 1 {
		t.Errorf("build requests for the given id = %d, want 1", got)
	}
	if len(ui.selection) != 0 {
		t.Errorf("selection was offered %v, want none", ui.selection)
	}
}

// TestSelectProductIDFromGameName covers the game-name path: the account is
// listed, a single match is used directly, and the resolved id is what the
// builds request names.
func TestSelectProductIDFromGameName(t *testing.T) {
	srv := newFixtureServer(t)
	d := newOfflineDownloader(t, srv.Server, galaxyTestConfig(t, "score"), newFakeConsole())

	if _, err := d.ShowBuilds(context.Background(), "Some", ""); err != nil {
		t.Fatalf("ShowBuilds: %v", err)
	}
	if got := srv.seen("/account/getFilteredProducts"); got != 1 {
		t.Errorf("the account was listed %d times, want 1", got)
	}
	if got := srv.seen("/products/555/os/windows/builds"); got != 1 {
		t.Errorf("build requests for the resolved id = %d, want 1", got)
	}
}

func TestSelectProductIDNoMatch(t *testing.T) {
	srv := newFixtureServer(t)
	srv.setProducts(fixtureProductsNone)
	d := newOfflineDownloader(t, srv.Server, galaxyTestConfig(t, "score"), newFakeConsole())

	res, err := d.ShowBuilds(context.Background(), "Nothing", "")
	if err != nil {
		t.Fatalf("ShowBuilds: %v", err)
	}
	if res.Notice.Text != msgNoProducts || !res.Notice.Err {
		t.Errorf("notice = %+v, want the stderr no-products message", res.Notice)
	}
	if got := srv.seen("/builds"); got != 0 {
		t.Errorf("build requests = %d, want none", got)
	}
}

// TestSelectProductIDInteractive covers the multi-match path: the console is
// offered the candidates and the index it returns selects the product.
func TestSelectProductIDInteractive(t *testing.T) {
	srv := newFixtureServer(t)
	srv.setProducts(fixtureProductsTwo)
	ui := newFakeConsole()
	ui.selected = 1
	d := newOfflineDownloader(t, srv.Server, galaxyTestConfig(t, "score"), ui)

	if _, err := d.ShowBuilds(context.Background(), "Some", ""); err != nil {
		t.Fatalf("ShowBuilds: %v", err)
	}
	if want := "Some Game A,Some Game B"; strings.Join(ui.selection, ",") != want {
		t.Errorf("offered %q, want %q", strings.Join(ui.selection, ","), want)
	}
	if got := srv.seen("/products/556/os/windows/builds"); got != 1 {
		t.Errorf("build requests for the chosen id = %d, want 1", got)
	}
}

// TestSelectProductIDUnanswerable covers the two ways a selection can fail to
// happen: no console to ask (the C++ isatty check) and an index the console
// should never return.
func TestSelectProductIDUnanswerable(t *testing.T) {
	cases := []struct {
		name string
		ui   *fakeConsole
	}{
		{name: "no console", ui: &fakeConsole{interactive: false, selectionErr: errors.New("no terminal")}},
		{name: "index out of range", ui: &fakeConsole{interactive: true, selected: 7}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := newFixtureServer(t)
			srv.setProducts(fixtureProductsTwo)
			d := newOfflineDownloader(t, srv.Server, galaxyTestConfig(t, "score"), c.ui)

			res, err := d.ShowBuilds(context.Background(), "Some", "")
			if err != nil {
				t.Fatalf("ShowBuilds: %v", err)
			}
			if res.Notice.Text != msgNoSelection || !res.Notice.Err {
				t.Errorf("notice = %+v, want the stderr no-selection message", res.Notice)
			}
			if got := srv.seen("/builds"); got != 0 {
				t.Errorf("build requests = %d, want none", got)
			}
		})
	}
}

// TestShowBuildsShapeErrors covers the protocol-shape tier: a missing or null
// list runs on as an empty one, a wrong shape fails.
func TestShowBuildsShapeErrors(t *testing.T) {
	srv := newFixtureServer(t)
	srv.setBuilds(`{"items":{}}`)
	d := newOfflineDownloader(t, srv.Server, galaxyTestConfig(t, "score"), newFakeConsole())

	if _, err := d.ShowBuilds(context.Background(), fixtureProductID, ""); err == nil {
		t.Error("a non-array items value must be an error")
	}
}

// TestEndpointNamesShapeErrors covers the tier on the secure link document: a
// missing or null "urls" is no endpoints, a wrong shape is an error.
func TestEndpointNamesShapeErrors(t *testing.T) {
	if names, err := endpointNames(map[string]any{}); err != nil || names != nil {
		t.Errorf("endpointNames(empty) = %v, %v; want no names and no error", names, err)
	}
	if names, err := endpointNames(map[string]any{"urls": nil}); err != nil || names != nil {
		t.Errorf("endpointNames(null) = %v, %v; want no names and no error", names, err)
	}
	if _, err := endpointNames(map[string]any{"urls": map[string]any{}}); err == nil {
		t.Error("a non-array urls value must be an error")
	}
	if _, err := endpointNames(map[string]any{"urls": []any{"not an object"}}); err == nil {
		t.Error("a non-object entry must be an error")
	}
}
