package core

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nekrozis/goggo/internal/config"
	"github.com/nekrozis/goggo/internal/gamedetails"
)

// The fixture answers the documents one acquisition run reads: the product
// documents, the DLC expansion, the owned-games list, the filtered-product list
// the name lookup uses, the downlink documents and the credential refresh. Only
// the transport is doubled, as elsewhere in this package.
type gameInfoFixture struct {
	*httptest.Server

	mu       sync.Mutex
	requests []string
	products map[string]string
	expanded string
	owned    string
	list     string
	failures map[string]int
	probe    *requestProbe
}

func newGameInfoFixture(t *testing.T) *gameInfoFixture {
	t.Helper()
	f := &gameInfoFixture{
		products: map[string]string{},
		owned:    `{"owned":[]}`,
		list:     `{"page":1,"totalPages":1,"products":[]}`,
		failures: map[string]int{},
	}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		f.mu.Lock()
		f.requests = append(f.requests, path)
		status := f.failures[path]
		doc, isProduct := f.products[productDocumentID(path)]
		expanded, owned, list, probe := f.expanded, f.owned, f.list, f.probe
		f.mu.Unlock()

		if probe != nil && isProduct {
			defer probe.enter()()
		}
		switch {
		case status != 0:
			http.Error(w, "fixture failure", status)
		case path == "/token":
			// The refresh answer the credential store stores (auth.Refresh).
			_, _ = w.Write([]byte(`{"access_token":"new","refresh_token":"new","expires_in":3600,"user_id":"1"}`))
		case path == "/www/user/data/games":
			_, _ = w.Write([]byte(owned))
		case path == "/www/account/getFilteredProducts":
			_, _ = w.Write([]byte(list))
		case path == "/dlc-expanded":
			_, _ = w.Write([]byte(expanded))
		case isProduct:
			_, _ = w.Write([]byte(doc))
		case strings.HasPrefix(path, "/dl/"):
			// One downlink document per file entry: the url it names is what
			// the conversion derives the file's path from.
			name := strings.TrimPrefix(path, "/dl/")
			_, _ = w.Write([]byte(`{"downlink":"https://cdn.gog.com/games/some-game/` + name + `"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

// productDocumentID returns the id when path is exactly a product document
// request, and "" otherwise: the build list and the batch form have other
// paths.
func productDocumentID(path string) string {
	rest, ok := strings.CutPrefix(path, "/products/")
	if !ok || rest == "" || strings.ContainsAny(rest, "/?") {
		return ""
	}
	return rest
}

func (f *gameInfoFixture) setProduct(id, doc string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.products[id] = doc
}

func (f *gameInfoFixture) setExpanded(docs ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.expanded = "[" + strings.Join(docs, ",") + "]"
}

func (f *gameInfoFixture) setOwned(ids ...string) {
	quoted := make([]string, 0, len(ids))
	for _, id := range ids {
		quoted = append(quoted, `"`+id+`"`)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.owned = `{"owned":[` + strings.Join(quoted, ",") + `]}`
}

// setOwnedBody pins the raw owned-games answer, for a shape the helper cannot
// express (a null or a scalar member).
func (f *gameInfoFixture) setOwnedBody(body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.owned = body
}

func (f *gameInfoFixture) setList(products ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.list = `{"page":1,"totalPages":1,"products":[` + strings.Join(products, ",") + `]}`
}

func (f *gameInfoFixture) setFailure(path string, status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failures[path] = status
}

func (f *gameInfoFixture) setProbe(p *requestProbe) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.probe = p
}

// seen counts the recorded requests whose path is exactly want.
func (f *gameInfoFixture) seen(want string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	var n int
	for _, path := range f.requests {
		if path == want {
			n++
		}
	}
	return n
}

// count counts the recorded requests whose path contains want.
func (f *gameInfoFixture) count(want string) int {
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

// paths returns a copy of the recorded request paths, in order.
func (f *gameInfoFixture) paths() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.requests...)
}

// requestProbe holds the product-document handlers until the test has seen a
// given number of requests in flight at once, so a worker-count assertion does
// not depend on how fast the machine is.
type requestProbe struct {
	target int

	mu       sync.Mutex
	inFlight int
	peak     int
	reached  chan struct{}
	release  chan struct{}
	once     sync.Once
	openOnce sync.Once
}

func newRequestProbe(target int) *requestProbe {
	return &requestProbe{
		target:  target,
		reached: make(chan struct{}),
		release: make(chan struct{}),
	}
}

// enter registers one in-flight request, holds it until the test opens the
// gate, and returns the release function the handler defers.
func (p *requestProbe) enter() func() {
	p.mu.Lock()
	p.inFlight++
	if p.inFlight > p.peak {
		p.peak = p.inFlight
	}
	if p.inFlight >= p.target {
		p.once.Do(func() { close(p.reached) })
	}
	p.mu.Unlock()

	// The request is not answered until the test says so, which is what makes
	// "this many were in flight at once" an observation rather than a race.
	<-p.release

	return func() {
		p.mu.Lock()
		p.inFlight--
		p.mu.Unlock()
	}
}

// open lets every blocked handler through.
func (p *requestProbe) open() { p.openOnce.Do(func() { close(p.release) }) }

func (p *requestProbe) peakNow() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.peak
}

// waitForPeak waits until target requests are in flight at once and then lets
// them through. A run with fewer workers than target never gets there, which is
// the failure this reports.
func (p *requestProbe) waitForPeak(t *testing.T, target int) {
	t.Helper()
	select {
	case <-p.reached:
	case <-time.After(10 * time.Second):
		p.open()
		t.Fatalf("only %d product requests were ever in flight, want %d", p.peakNow(), target)
	}
	p.open()
}

// gameInfoConfig is the configuration one acquisition runs against: the
// defaults plus the installer platform and language the conversion gates on
// (the front end normally supplies both).
func gameInfoConfig(t *testing.T) config.Config {
	t.Helper()
	return gameInfoConfigIn(t, t.TempDir())
}

func gameInfoConfigIn(t *testing.T, dir string) config.Config {
	t.Helper()
	cfg := config.NewConfig(dir, dir)
	cfg.DownloadConfig.GalaxyPlatform = config.PlatformWindows
	cfg.DownloadConfig.InstallerPlatform = config.PlatformWindows
	cfg.DownloadConfig.InstallerLanguage = config.LangEN
	return cfg
}

// newGameInfoDownloader builds an offline run whose credentials are still
// valid, so the refresh path stays out of the way. The refresh test builds its
// own, with an empty store.
func newGameInfoDownloader(t *testing.T, f *gameInfoFixture, cfg config.Config) *Downloader {
	t.Helper()
	d := newOfflineDownloader(t, f.Server, cfg, newFakeConsole())
	d.token.SetJSON(map[string]any{"access_token": "a", "refresh_token": "r", "expires_in": 3600})
	return d
}

// The document builders below render the members the expansion, the conversion
// and the DLC gate actually read — in the shapes the live API sends them
// (DEFECT-GD3-1 evidence): the product and DLC ids are JSON numbers, a file
// entry's id is a string on installers ("en1installer0") and a number on
// bonus content (13403), so the acquisition chain exercises both forms
// end-to-end through the real conversion and expansion code.

// gameInfoFileEntry is one file of a downloads entry; its downlink is the url
// the resolver is asked about, and the fixture answers it. numericID selects
// the bonus-content shape; installers pass a false.
func gameInfoFileEntry(file string, numericID bool) string {
	id := `"` + file + `"`
	if numericID {
		id = "13403"
	}
	return `{"id":` + id + `,"downlink":"https://api.gog.com/dl/` + file + `","size":10}`
}

// gameInfoNode is one downloads entry. Extras carry no platform or language,
// which is how the conversion tells them apart (an empty osName) — and, like
// the live bonus-content entries, their file ids are numbers.
func gameInfoNode(name, osName, language string) string {
	node := `{"name":"` + name + `","version":"1.0","count":1,"total_size":10,` +
		`"files":[` + gameInfoFileEntry(name, osName == "") + `]`
	if osName != "" {
		node += `,"os":"` + osName + `","language":"` + language + `"`
	}
	return node + `}`
}

// gameInfoDoc renders a product document. dlcIDs with an expandedURL is what
// makes the expansion run; no ids means no dlcs member at all.
func gameInfoDoc(id, slug, title string, installers, extras []string, dlcIDs []string, expandedURL string) string {
	doc := `{"id":` + id + `,"slug":"` + slug + `","title":"` + title + `",` +
		`"images":{"icon":"//images.gog.com/icon.png","logo":"//images.gog.com/logo_glx_logo.jpg"},` +
		`"downloads":{"installers":[` + strings.Join(installers, ",") + `],` +
		`"bonus_content":[` + strings.Join(extras, ",") + `],` +
		`"patches":[],"language_packs":[]}`
	if len(dlcIDs) != 0 {
		ids := make([]string, 0, len(dlcIDs))
		for _, id := range dlcIDs {
			ids = append(ids, `{"id":`+id+`}`)
		}
		doc += `,"dlcs":{"products":[` + strings.Join(ids, ",") + `],` +
			`"expanded_all_products_url":"` + expandedURL + `"}`
	}
	return doc + `}`
}

// windowsInstaller is the one-node vector most tests need.
func windowsInstaller(file string) []string {
	return []string{gameInfoNode(file, "windows", "en")}
}

// gameInfoDocArrayDLCs is the DEFECT-GD3-2 shape: "dlcs":[] (what the live API
// sends for most products of a probed account), spelled through the same
// numeric-id wire format as every other acquisition fixture.
func gameInfoDocArrayDLCs(id, slug, title string, installers []string) string {
	return `{"id":` + id + `,"slug":"` + slug + `","title":"` + title + `",` +
		`"images":{"icon":"//images.gog.com/icon.png","logo":"//images.gog.com/logo.jpg"},` +
		`"downloads":{"installers":[` + strings.Join(installers, ",") + `],` +
		`"bonus_content":[],"patches":[],"language_packs":[]},` +
		`"dlcs":[]}`
}

// TestGameDetailsSkipsAnArrayShapedDLCsMember locks D52 end-to-end: a product
// whose dlcs is the empty array acquires like any DLC-less product — no error,
// no expansion request, zero DLCs — instead of failing the run at Product().
// This is the exact live shape (heroes_of_might_and_magic_3_complete_edition)
// that the pre-fix build rejected.
func TestGameDetailsSkipsAnArrayShapedDLCsMember(t *testing.T) {
	f := newGameInfoFixture(t)
	f.setProduct("111", gameInfoDocArrayDLCs("111", "some_game", "Some Game", windowsInstaller("setup.exe")))

	d := newGameInfoDownloader(t, f, gameInfoConfig(t))
	res, err := d.GameDetails(context.Background(), GameDetailsRequest{Products: []string{"111"}})
	if err != nil {
		t.Fatalf("GameDetails: %v", err)
	}
	if len(res) != 1 || len(res[0].DLCs) != 0 {
		t.Fatalf("results = %+v, want the product with zero dlcs", res)
	}
	if len(res[0].Installers) != 1 {
		t.Errorf("installers = %d, want the one file", len(res[0].Installers))
	}
	if got := f.count("/dlc-expanded"); got != 0 {
		t.Errorf("expansion requests = %d, want none: nothing enters the block", got)
	}
}

// TestGameDetailsMixesDLCBearingAndArrayProducts locks that one array-shaped
// product in a batch is neither a failure nor a contagion: the dict-shaped
// product still expands and filters through its DLCs, the array-shaped one
// comes back DLC-less, and the fail-fast pool leaves the completed siblings in
// the result set (a skipped expansion is not an error to cancel).
func TestGameDetailsMixesDLCBearingAndArrayProducts(t *testing.T) {
	f := newGameInfoFixture(t)
	f.setOwned("200")
	f.setProduct("100", gameInfoDoc("100", "base_game", "Base Game", windowsInstaller("base.exe"), nil,
		[]string{"200"}, f.URL+"/dlc-expanded"))
	f.setExpanded(gameInfoDoc("200", "base_game_dlc", "Base Game DLC", windowsInstaller("dlc.exe"), nil, nil, ""))
	f.setProduct("300", gameInfoDocArrayDLCs("300", "array_game", "Array Game", windowsInstaller("setup.exe")))

	d := newGameInfoDownloader(t, f, gameInfoConfig(t))
	res, err := d.GameDetails(context.Background(), GameDetailsRequest{Products: []string{"100", "300"}})
	if err != nil {
		t.Fatalf("GameDetails: %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("results = %d, want both products", len(res))
	}
	// Sorted by gamename: "array_game" < "base_game".
	var arrayGame, dictGame *gamedetails.GameDetails
	for i := range res {
		switch res[i].Gamename {
		case "array_game":
			arrayGame = &res[i]
		case "base_game":
			dictGame = &res[i]
		}
	}
	if arrayGame == nil || dictGame == nil {
		t.Fatalf("gamenames = %q/%q, want array_game and base_game", res[0].Gamename, res[1].Gamename)
	}
	if len(dictGame.DLCs) != 1 {
		t.Errorf("base game dlcs = %+v, want the owned DLC expanded", dictGame.DLCs)
	}
	if len(arrayGame.DLCs) != 0 || f.count("/dlc-expanded") != 1 {
		t.Errorf("array game dlcs = %d, expansion requests = %d, want 0 and 1",
			len(arrayGame.DLCs), f.count("/dlc-expanded"))
	}
}

// TestGameDetailsSortsByGamename locks the ordering: the result follows the
// gamename, not the request order and not the order the workers finished in
// (downloader.cpp:499). Each product is read once, and each of its files
// through the resolver once.
func TestGameDetailsSortsByGamename(t *testing.T) {
	f := newGameInfoFixture(t)
	for _, p := range []struct{ id, slug, title string }{
		{"300", "zulu_game", "Zulu"},
		{"100", "alpha_game", "Alpha"},
		{"200", "mike_game", "Mike"},
	} {
		f.setProduct(p.id, gameInfoDoc(p.id, p.slug, p.title, windowsInstaller(p.slug+".exe"), nil, nil, ""))
	}
	d := newGameInfoDownloader(t, f, gameInfoConfig(t))

	res, err := d.GameDetails(context.Background(), GameDetailsRequest{
		Products: []string{"300", "100", "200"},
	})
	if err != nil {
		t.Fatalf("GameDetails: %v", err)
	}

	want := []string{"alpha_game", "mike_game", "zulu_game"}
	if len(res) != len(want) {
		t.Fatalf("results = %d, want %d", len(res), len(want))
	}
	// The fixture documents carry their id as a JSON number (the live API's
	// shape); the conversion's ProductID must be the stringified form.
	wantIDs := []string{"100", "200", "300"}
	for i, gamename := range want {
		if res[i].Gamename != gamename {
			t.Errorf("result %d = %q, want %q", i, res[i].Gamename, gamename)
		}
		if res[i].ProductID != wantIDs[i] {
			t.Errorf("%s: ProductID = %q, want %q", gamename, res[i].ProductID, wantIDs[i])
		}
		if len(res[i].Installers) != 1 {
			t.Errorf("%s: installers = %d, want the one file", gamename, len(res[i].Installers))
		}
	}

	for _, id := range []string{"100", "200", "300"} {
		if got := f.seen("/products/" + id); got != 1 {
			t.Errorf("product %s fetched %d times, want once", id, got)
		}
	}
	if got := f.count("/dl/"); got != 3 {
		t.Errorf("downlink requests = %d, want one per file", got)
	}
}

// TestGameDetailsIsIndependentOfThreadCount locks the deterministic part of the
// concurrency: one worker and four workers produce the same result and touch the
// same documents. It is the property the plan's acceptance calls out, and the
// reason the ordering is not left to the scheduler.
func TestGameDetailsIsIndependentOfThreadCount(t *testing.T) {
	f := newGameInfoFixture(t)
	for _, p := range []struct{ id, slug string }{
		{"100", "alpha_game"}, {"200", "mike_game"}, {"300", "zulu_game"}, {"400", "oscar_game"},
	} {
		f.setProduct(p.id, gameInfoDoc(p.id, p.slug, p.slug, windowsInstaller(p.slug+".exe"), nil, nil, ""))
	}
	cfg := gameInfoConfig(t)

	products := []string{"400", "300", "200", "100"}
	run := func(threads int) []gamedetails.GameDetails {
		t.Helper()
		d := newGameInfoDownloader(t, f, cfg)
		res, err := d.GameDetails(context.Background(), GameDetailsRequest{
			Products:    products,
			InfoThreads: threads,
		})
		if err != nil {
			t.Fatalf("GameDetails with %d threads: %v", threads, err)
		}
		return res
	}

	one := run(1)
	if f.count("/products/") != 4 {
		t.Fatalf("product requests after the single-worker run = %d, want 4", f.count("/products/"))
	}
	four := run(4)

	if !reflect.DeepEqual(one, four) {
		t.Errorf("results differ between one and four workers:\none:  %+v\nfour: %+v", one, four)
	}
	if got := f.count("/products/"); got != 8 {
		t.Errorf("product requests after both runs = %d, want 8 (once per product per run)", got)
	}
}

// TestGameDetailsWorkerCount locks the worker count: the request's value wins,
// the setting is the fallback, the work caps it, and zero — which is what the
// setting holds while the option is unregistered — means the documented default
// rather than no work at all.
func TestGameDetailsWorkerCount(t *testing.T) {
	cases := []struct {
		name      string
		products  int
		requested int
		setting   uint32
		want      int
	}{
		{name: "the request wins", products: 4, requested: 2, setting: 3, want: 2},
		{name: "the setting is the fallback", products: 4, requested: 0, setting: 3, want: 3},
		{name: "the default setting", products: 6, requested: 0, setting: 0, want: defaultInfoThreads},
		{name: "the work caps the count", products: 2, requested: 8, setting: 0, want: 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newGameInfoFixture(t)
			products := make([]string, 0, c.products)
			for i := 0; i < c.products; i++ {
				id := strconv.Itoa(100 + i)
				f.setProduct(id, gameInfoDoc(id, "game_"+id, "Game "+id, windowsInstaller("setup.exe"), nil, nil, ""))
				products = append(products, id)
			}

			cfg := gameInfoConfig(t)
			cfg.InfoThreads = c.setting

			probe := newRequestProbe(c.want)
			f.setProbe(probe)
			d := newGameInfoDownloader(t, f, cfg)

			var (
				res []gamedetails.GameDetails
				err error
			)
			done := make(chan struct{})
			go func() {
				defer close(done)
				res, err = d.GameDetails(context.Background(), GameDetailsRequest{
					Products:    products,
					InfoThreads: c.requested,
				})
			}()
			probe.waitForPeak(t, c.want)
			<-done

			if err != nil {
				t.Fatalf("GameDetails: %v", err)
			}
			if len(res) != c.products {
				t.Errorf("results = %d, want %d", len(res), c.products)
			}
			if got := probe.peakNow(); got != c.want {
				t.Errorf("requests in flight = %d, want %d", got, c.want)
			}
		})
	}
}

// TestGameDetailsOwnedGating locks where the owned set comes from: the include
// mask asks for DLC content, so that is when the account's list is read and the
// DLCs are filtered against it. Without the DLC bit neither happens
// (review GD3 §3, plan divergence 3).
func TestGameDetailsOwnedGating(t *testing.T) {
	const (
		baseID = "100"
		dlcID  = "200"
	)

	cases := []struct {
		name         string
		include      uint32
		owned        string
		wantOwned    int
		wantDLCs     int
		wantOwnedSet string
	}{
		{
			name:      "an owned DLC survives",
			include:   config.IncludeAllMask(),
			owned:     `{"owned":["100","200"]}`,
			wantOwned: 1,
			wantDLCs:  1,
		},
		{
			name:      "an unowned DLC is dropped",
			include:   config.IncludeAllMask(),
			owned:     `{"owned":["100","999"]}`,
			wantOwned: 1,
			wantDLCs:  0,
		},
		{
			name:      "no list means no filtering",
			include:   config.IncludeAllMask(),
			owned:     `{"owned":[]}`,
			wantOwned: 1,
			wantDLCs:  1,
		},
		{
			name:      "without the DLC bit nothing is asked or filtered",
			include:   config.IncludeAllMask() &^ config.GFDLC,
			owned:     `{"owned":["100"]}`,
			wantOwned: 0,
			wantDLCs:  0,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newGameInfoFixture(t)
			f.setOwnedBody(c.owned)
			f.setProduct(baseID, gameInfoDoc(baseID, "base_game", "Base Game",
				windowsInstaller("base.exe"), nil, []string{dlcID}, f.URL+"/dlc-expanded"))
			f.setExpanded(gameInfoDoc(dlcID, "base_game_dlc", "Base Game DLC",
				windowsInstaller("dlc.exe"), nil, nil, ""))

			cfg := gameInfoConfig(t)
			cfg.DownloadConfig.Include = c.include
			d := newGameInfoDownloader(t, f, cfg)

			res, err := d.GameDetails(context.Background(), GameDetailsRequest{Products: []string{baseID}})
			if err != nil {
				t.Fatalf("GameDetails: %v", err)
			}
			if got := f.seen("/www/user/data/games"); got != c.wantOwned {
				t.Errorf("owned requests = %d, want %d", got, c.wantOwned)
			}
			if len(res) != 1 {
				t.Fatalf("results = %d, want the one product", len(res))
			}
			if got := len(res[0].DLCs); got != c.wantDLCs {
				t.Errorf("dlcs = %d, want %d", got, c.wantDLCs)
			}
			// Ruling C: the fetch is the faithful superset — Product expands the
			// DLCs whatever the mask says, and the owned gate is applied by the
			// conversion afterwards. So the expansion request count is one here
			// even when nothing survives it.
			if got := f.count("/dlc-expanded"); got != 1 {
				t.Errorf("expansion requests = %d, want exactly one", got)
			}
		})
	}
}

// TestGameDetailsPriorityFilterAppliesToDLCSubtree locks the filter chain on
// the result: with both platforms accepted by the conversion, the priority list
// is what keeps the best-ranked entries — inside the DLC subtree too
// (gamedetails.cpp:19-34).
func TestGameDetailsPriorityFilterAppliesToDLCSubtree(t *testing.T) {
	const (
		baseID = "100"
		dlcID  = "200"
	)

	f := newGameInfoFixture(t)
	bothPlatforms := []string{
		gameInfoNode("windows.exe", "windows", "en"),
		gameInfoNode("linux.sh", "linux", "en"),
	}
	f.setProduct(baseID, gameInfoDoc(baseID, "base_game", "Base Game", bothPlatforms, nil,
		[]string{dlcID}, f.URL+"/dlc-expanded"))
	f.setExpanded(gameInfoDoc(dlcID, "base_game_dlc", "Base Game DLC", bothPlatforms, nil, nil, ""))

	cfg := gameInfoConfig(t)
	// The conversion accepts both platforms; the priority list prefers Linux.
	cfg.DownloadConfig.InstallerPlatform = config.PlatformWindows | config.PlatformLinux
	cfg.DownloadConfig.PlatformPriority = []uint32{config.PlatformLinux, config.PlatformWindows}
	d := newGameInfoDownloader(t, f, cfg)

	res, err := d.GameDetails(context.Background(), GameDetailsRequest{Products: []string{baseID}})
	if err != nil {
		t.Fatalf("GameDetails: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("results = %d, want the one product", len(res))
	}
	if len(res[0].Installers) != 1 || res[0].Installers[0].Platform != config.PlatformLinux {
		t.Errorf("installers = %+v, want the single best-ranked entry", res[0].Installers)
	}
	if len(res[0].DLCs) != 1 {
		t.Fatalf("dlcs = %d, want the subtree", len(res[0].DLCs))
	}
	if dlc := res[0].DLCs[0].Installers; len(dlc) != 1 || dlc[0].Platform != config.PlatformLinux {
		t.Errorf("dlc installers = %+v, want the filter to reach the subtree", dlc)
	}
}

// TestGameDetailsTypeFilterDropsWhatTheCompositeGateConverted locks the second
// half of the filter chain on a real acquisition: the conversion gates each
// vector by its COMPOSITE mask, so a mask holding only the DLC installer bit
// still converts the base installers — and FilterWithType is what removes them
// afterwards, upstream's two-step exactly (galaxyapi.cpp:404-425 and
// gamedetails.cpp:268-281).
func TestGameDetailsTypeFilterDropsWhatTheCompositeGateConverted(t *testing.T) {
	const (
		baseID = "100"
		dlcID  = "200"
	)

	f := newGameInfoFixture(t)
	f.setProduct(baseID, gameInfoDoc(baseID, "base_game", "Base Game",
		windowsInstaller("base.exe"), []string{gameInfoNode("wallpaper.zip", "", "")},
		[]string{dlcID}, f.URL+"/dlc-expanded"))
	f.setExpanded(gameInfoDoc(dlcID, "base_game_dlc", "Base Game DLC", windowsInstaller("dlc.exe"), nil, nil, ""))

	cfg := gameInfoConfig(t)
	// Only DLC installers are wanted. The conversion still converts the base
	// installers (the composite installer mask matches), so the type filter is
	// the only thing that can remove them.
	cfg.DownloadConfig.Include = config.GFDLCInstaller
	d := newGameInfoDownloader(t, f, cfg)

	res, err := d.GameDetails(context.Background(), GameDetailsRequest{Products: []string{baseID}})
	if err != nil {
		t.Fatalf("GameDetails: %v", err)
	}
	if len(res[0].Installers) != 0 {
		t.Errorf("base installers = %+v, want none after the type filter", res[0].Installers)
	}
	if len(res[0].Extras) != 0 {
		t.Errorf("extras = %+v, want none: the mask has no extras bit", res[0].Extras)
	}
	if len(res[0].DLCs) != 1 || len(res[0].DLCs[0].Installers) != 1 {
		t.Errorf("dlc installers = %+v, want the DLC installer kept", res[0].DLCs)
	}
}

// TestGameDetailsRefreshesBeforeTheFirstRequest locks the order upstream has
// (downloader.cpp:3731-3739): the credentials are refreshed before the product
// document is read, and once for the whole run — the shared refresher makes the
// other workers pay nothing.
func TestGameDetailsRefreshesBeforeTheFirstRequest(t *testing.T) {
	f := newGameInfoFixture(t)
	for _, id := range []string{"100", "200", "300"} {
		f.setProduct(id, gameInfoDoc(id, "game_"+id, "Game "+id, windowsInstaller("setup.exe"), nil, nil, ""))
	}
	// Expired credentials, with a refresh token to spend: that is what makes the
	// refresh run before the first product request. The refresh stores the new
	// token, so the configuration directory has to be there — Open normally
	// creates it, and this run goes straight to the command.
	cfg := gameInfoConfig(t)
	if err := os.MkdirAll(cfg.ConfigDirectory, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	d := newOfflineDownloader(t, f.Server, cfg, newFakeConsole())
	d.token.SetJSON(map[string]any{"access_token": "old", "refresh_token": "r", "expires_in": -1})

	res, err := d.GameDetails(context.Background(), GameDetailsRequest{
		Products: []string{"100", "200", "300"},
	})
	if err != nil {
		t.Fatalf("GameDetails: %v", err)
	}
	if len(res) != 3 {
		t.Fatalf("results = %d, want 3", len(res))
	}

	paths := f.paths()
	if got := f.seen("/token"); got != 1 {
		t.Errorf("refresh requests = %d, want one for the whole run", got)
	}
	// The order the rule locks is refresh before the first *product* request.
	// The account's owned list is a website call and carries no galaxy
	// credential, so it may come earlier.
	refreshed, requested := -1, -1
	for i, path := range paths {
		if path == "/token" && refreshed < 0 {
			refreshed = i
		}
		if strings.HasPrefix(path, "/products/") && requested < 0 {
			requested = i
		}
	}
	if refreshed < 0 {
		t.Fatalf("no credential refresh happened: %v", paths)
	}
	if requested < 0 {
		t.Fatalf("no product request was made: %v", paths)
	}
	if refreshed > requested {
		t.Errorf("the product request came before the refresh: %v", paths)
	}
}

// TestGameDetailsIsCompleteOrNothing locks the failure contract: every failure
// mode ends the run with an error and NO results, never with the products that
// happened to succeed. A caller that got a short list could not tell it from
// "this product has no files" (review GD3 §3).
func TestGameDetailsIsCompleteOrNothing(t *testing.T) {
	cases := []struct {
		name     string
		products []string
		setUp    func(t *testing.T, f *gameInfoFixture)
		wantErr  string
	}{
		{
			name:     "a product document that fails",
			products: []string{"100", "200"},
			setUp: func(t *testing.T, f *gameInfoFixture) {
				t.Helper()
				f.setProduct("100", gameInfoDoc("100", "ok_game", "Ok", windowsInstaller("setup.exe"), nil, nil, ""))
				f.setProduct("200", gameInfoDoc("200", "bad_game", "Bad", windowsInstaller("setup.exe"), nil, nil, ""))
				f.setFailure("/products/200", http.StatusInternalServerError)
			},
		},
		{
			name:     "a product document the conversion rejects",
			products: []string{"100", "200"},
			setUp: func(t *testing.T, f *gameInfoFixture) {
				t.Helper()
				f.setProduct("100", gameInfoDoc("100", "ok_game", "Ok", windowsInstaller("setup.exe"), nil, nil, ""))
				// downloads is an object everywhere else; a string is a shape
				// the conversion refuses.
				f.setProduct("200", `{"id":"200","slug":"bad_game","title":"Bad","downloads":"nope"}`)
			},
			wantErr: "downloads",
		},
		{
			name:     "a name that matches no product",
			products: []string{"no_such_game"},
			setUp:    func(t *testing.T, f *gameInfoFixture) {},
			wantErr:  msgNoProducts,
		},
		{
			name:     "no products at all",
			products: nil,
			setUp:    func(t *testing.T, f *gameInfoFixture) {},
			wantErr:  "no products requested",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newGameInfoFixture(t)
			c.setUp(t, f)
			d := newGameInfoDownloader(t, f, gameInfoConfig(t))

			res, err := d.GameDetails(context.Background(), GameDetailsRequest{Products: c.products})
			if err == nil {
				t.Fatalf("results = %+v, want a failure", res)
			}
			if res != nil {
				t.Errorf("results = %+v, want none: a partial set is not an answer", res)
			}
			if c.wantErr != "" && !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("error = %v, want it to mention %q", err, c.wantErr)
			}
		})
	}
}

// TestGameDetailsWritesNothing locks the purity of the layer: an acquisition
// run that does not have to refresh the credentials writes no file anywhere
// under the run's directories (review GD3 §6: no cache, no --save-* artifact,
// no makeFilepaths).
func TestGameDetailsWritesNothing(t *testing.T) {
	dir := t.TempDir()
	f := newGameInfoFixture(t)
	f.setProduct("100", gameInfoDoc("100", "alpha_game", "Alpha",
		windowsInstaller("setup.exe"), []string{gameInfoNode("wallpaper.zip", "", "")}, nil, ""))
	d := newGameInfoDownloader(t, f, gameInfoConfigIn(t, dir))

	if _, err := d.GameDetails(context.Background(), GameDetailsRequest{Products: []string{"100"}}); err != nil {
		t.Fatalf("GameDetails: %v", err)
	}

	var written []string
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		written = append(written, path)
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(written) != 1 { // the directory itself
		t.Errorf("the run wrote %v, want nothing", written[1:])
	}
}

// TestGameDetailsToleratesASkippedFile locks the one failure that is NOT fatal:
// a resolver that cannot resolve a file skips that file and the product is still
// produced (GD1's DownlinkResolver contract). The run stays complete, which is
// what keeps the fail-fast rule above from turning a single missing downlink
// into a failed acquisition.
func TestGameDetailsToleratesASkippedFile(t *testing.T) {
	f := newGameInfoFixture(t)
	f.setProduct("100", gameInfoDoc("100", "alpha_game", "Alpha", []string{
		gameInfoNode("good.exe", "windows", "en"),
		gameInfoNode("bad.exe", "windows", "en"),
	}, nil, nil, ""))
	f.setFailure("/dl/bad.exe", http.StatusNotFound)

	d := newGameInfoDownloader(t, f, gameInfoConfig(t))
	res, err := d.GameDetails(context.Background(), GameDetailsRequest{Products: []string{"100"}})
	if err != nil {
		t.Fatalf("GameDetails: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("results = %d, want the product", len(res))
	}
	if len(res[0].Installers) != 1 || !strings.Contains(res[0].Installers[0].Path, "good.exe") {
		t.Errorf("installers = %+v, want only the resolvable file", res[0].Installers)
	}
}
