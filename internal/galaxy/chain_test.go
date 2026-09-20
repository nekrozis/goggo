// Package galaxy_test drives the whole Galaxy content chain against a fake
// content-system/CDN: build listing → build manifest → depot selection → depot
// manifest → chunk hash → secure link → URL template → chunk download.
//
// It is a black-box test on purpose. The client is built through the public API
// and only the transport is doubled, so the URLs under test are the ones the
// package itself builds — endpoints, hosts and all. The alternative (reaching
// into the unexported endpoint block, as the unit tests do) would bypass exactly
// the wiring this test exists to check.
//
// Every path the CDN must answer is derived from the fixture's own bytes and
// hashes rather than written down: the chunk's md5 comes from one byte slice,
// HashToGalaxyPath turns hashes into paths, and both the handlers and the
// expected request sequence use the result. A mistake in the hash rule or in the
// chain therefore fails the test instead of matching a hand-written string.
//
// What this does NOT cover is deliberate: choosing a build (sorting, build ids),
// the DLC include filter, retries, resume, the small-files container and the
// downloader state machine are all out of scope here. The fixture picks items[0]
// and requires generation 2 as a stand-in for that selection logic.
//
// Decompression is out of scope as well: the chunk's uncompressed md5
// and size are structural placeholders, while the chain that is verified is
// compressedMd5 → galaxy path → manifest → download.
package galaxy_test

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/nekrozis/goggo/internal/config"
	"github.com/nekrozis/goggo/internal/galaxy"
	"github.com/nekrozis/goggo/internal/httpx"
)

const (
	chainProductID = "1207659150"
	chainGameName  = "gamename"
	chainBuildHash = "0123456789abcdef0123456789abcdef01234567"
	chainDepotHash = "89abcdef0123456789abcdef0123456789abcdef"
	chainToken     = "tok"
)

// chainChunk is the byte slice every piece of fixture content is derived from.
var chainChunk = []byte("goggo chain fixture chunk")

// recorded is one request the fake CDN saw.
type recorded struct {
	Query url.Values
	Path  string
	Auth  string
}

// chainFixture is the fake content-system plus CDN, with the client wired to it.
type chainFixture struct {
	srv       *httptest.Server
	client    *galaxy.Client
	plainHTTP *http.Client

	// Paths the fake CDN serves, all derived from the hashes above through
	// HashToGalaxyPath — the same rule the package applies to a manifest hash.
	buildManifestPath    string
	depotManifestPath    string
	depChunkPath         string // "ab/cd/<md5>" of the fixture chunk
	depotDepManifestPath string

	mu   sync.Mutex
	seen []recorded

	// depotLanguages/depotBitness are what the build manifest declares; the
	// mismatched fixture changes them so the filter can reject the depot.
	depotLanguages []string
	depotBitness   []string
}

// hostRewriteTransport points the production hosts at the test server. Only the
// scheme and host are replaced: path, query and headers stay exactly as the
// package built them, so the request under test is the production one.
type hostRewriteTransport struct{ target *url.URL }

func (t *hostRewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	u := *req.URL
	u.Scheme, u.Host = t.target.Scheme, t.target.Host
	clone.URL = &u
	return http.DefaultTransport.RoundTrip(clone)
}

// withMismatchedDepot makes the build manifest declare a depot that neither the
// language nor the architecture of the caller selects.
func withMismatchedDepot() func(*chainFixture) {
	return func(f *chainFixture) {
		f.depotLanguages = []string{"fr"}
		f.depotBitness = []string{"32"}
	}
}

func newChainFixture(t *testing.T, opts ...func(*chainFixture)) *chainFixture {
	t.Helper()
	f := &chainFixture{
		depotLanguages: []string{"en"},
		depotBitness:   []string{"64"},
	}
	for _, opt := range opts {
		opt(f)
	}

	// The chunk's md5 comes from the fixture bytes, and its path from the rule
	// under test: a wrong hash-to-path mapping must break this test.
	sum := md5.Sum(chainChunk)
	chunkMD5 := hex.EncodeToString(sum[:])
	f.depChunkPath = galaxy.HashToGalaxyPath(chunkMD5)
	f.buildManifestPath = "/content-system/v2/meta/" + galaxy.HashToGalaxyPath(chainBuildHash)
	f.depotManifestPath = "/content-system/v2/meta/" + galaxy.HashToGalaxyPath(chainDepotHash)
	f.depotDepManifestPath = "/content-system/v2/dependencies/meta/" + galaxy.HashToGalaxyPath(chainDepotHash)

	mux := http.NewServeMux()
	mux.HandleFunc("/products/"+chainProductID+"/os/windows/builds", func(w http.ResponseWriter, r *http.Request) {
		// The link ends in the build hash, which is how the download path
		// derives it.
		fmt.Fprintf(w, `{"items":[{"generation":2,"build_id":"b1","link":%q}]}`,
			"https://cdn.gog.com"+f.buildManifestPath)
	})
	mux.HandleFunc(f.buildManifestPath, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"baseProductId":%q,"depots":[{"languages":[%s],"osBitness":[%s],`+
			`"manifest":%q,"productId":%q}]}`,
			chainProductID, quoteAll(f.depotLanguages), quoteAll(f.depotBitness), chainDepotHash, chainProductID)
	})
	mux.HandleFunc(f.depotManifestPath, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, depotManifestBody("game\\data.bin", chunkMD5))
	})
	mux.HandleFunc(f.depotDepManifestPath, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, depotManifestBody("dep\\dep.bin", chunkMD5))
	})
	mux.HandleFunc("/products/"+chainProductID+"/secure_link", func(w http.ResponseWriter, r *http.Request) {
		// The listed endpoint is deliberately NOT first: the ranking must move it.
		fmt.Fprint(w, `{"urls":[`+
			`{"endpoint_name":"cdnAlt","url_format":"https://cdn.gog.com/alt{path}","parameters":{"path":""}},`+
			`{"endpoint_name":"cdnMain","url_format":"https://cdn.gog.com/chunks{path}","parameters":{"path":""}}]}`)
	})
	mux.HandleFunc("/open_link", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"urls":[{"endpoint_name":"cdnMain",`+
			`"url_format":"https://cdn.gog.com/chunks{path}","parameters":{"path":"/%s"}}]}`, f.depChunkPath)
	})
	mux.HandleFunc("/chunks/"+f.depChunkPath, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(chainChunk)
	})

	f.srv = httptest.NewServer(f.recording(mux))
	t.Cleanup(f.srv.Close)

	target, err := url.Parse(f.srv.URL)
	if err != nil {
		t.Fatalf("parse %q: %v", f.srv.URL, err)
	}
	f.plainHTTP = &http.Client{Transport: &hostRewriteTransport{target: target}}

	hx, err := httpx.New(httpx.Config{
		UserAgent: "goggo-test/1.0",
		// The client is injected, so the package keeps building production URLs.
		HTTPClient:  f.plainHTTP,
		RetryPolicy: httpx.DefaultPolicy(1, 0),
	})
	if err != nil {
		t.Fatalf("httpx.New: %v", err)
	}
	galaxyCfg := config.NewGalaxyConfig()
	galaxyCfg.SetJSON(map[string]any{"access_token": chainToken, "expires_in": 3600})
	f.client, err = galaxy.New(hx, galaxyCfg)
	if err != nil {
		t.Fatalf("galaxy.New: %v", err)
	}
	return f
}

// recording wraps the fixture handler so every request is kept in order.
func (f *chainFixture) recording(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.seen = append(f.seen, recorded{Path: r.URL.Path, Query: r.URL.Query(), Auth: r.Header.Get("Authorization")})
		f.mu.Unlock()
		next.ServeHTTP(w, r)
	})
}

func (f *chainFixture) calls() []recorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recorded(nil), f.seen...)
}

// fetch retrieves a production-shaped URL through the same rewriting client.
func (f *chainFixture) fetch(t *testing.T, rawURL string) []byte {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, rawURL, nil)
	if err != nil {
		t.Fatalf("NewRequest(%q): %v", rawURL, err)
	}
	resp, err := f.plainHTTP.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", rawURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d", rawURL, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", rawURL, err)
	}
	return body
}

// firstDepotEntry fetches the build manifest through the client and returns the
// depot entry the download path would expand.
//
// Picking depots[0] stands in for the real selection logic (build ids, sorting,
// the DLC include filter); it is a harness simplification.
func (f *chainFixture) firstDepotEntry(t *testing.T) map[string]any {
	t.Helper()
	manifest, err := f.client.ManifestV2(context.Background(), chainBuildHash, false)
	if err != nil {
		t.Fatalf("ManifestV2: %v", err)
	}
	depots := mustArray(t, manifest["depots"], "depots")
	return mustObject(t, depots[0], "depots[0]")
}

// TestChainProductDownload walks the whole product chain and checks both the
// pieces and the order they are asked for.
func TestChainProductDownload(t *testing.T) {
	f := newChainFixture(t)
	ctx := context.Background()

	// 1. Build listing.
	builds, err := f.client.ProductBuilds(ctx, chainProductID, "windows", "2")
	if err != nil {
		t.Fatalf("ProductBuilds: %v", err)
	}
	items := mustArray(t, builds["items"], "items")
	first := mustObject(t, items[0], "items[0]")
	if first["generation"] != float64(2) {
		t.Fatalf("generation = %v, want 2", first["generation"])
	}
	link := mustString(t, first["link"], "items[0].link")
	hash := link[strings.LastIndex(link, "/")+1:]
	if hash != chainBuildHash {
		t.Fatalf("build hash = %q, want %q", hash, chainBuildHash)
	}

	// 2 + 3. Build manifest, then depot selection (the depot manifest follows).
	depot := f.firstDepotEntry(t)
	depotItems, err := f.client.FilteredDepotItems(ctx, depot, "en", "64", galaxy.DepotOptions{})
	if err != nil {
		t.Fatalf("FilteredDepotItems: %v", err)
	}
	if len(depotItems) != 1 {
		t.Fatalf("depot items = %d, want 1", len(depotItems))
	}
	if depotItems[0].Path != "game/data.bin" {
		t.Errorf("item path = %q, want the backslashes rewritten", depotItems[0].Path)
	}

	// 4. Chunk hash → galaxy path.
	chunk := depotItems[0].Chunks[0]
	if got := galaxy.HashToGalaxyPath(chunk.CompressedMD5); got != f.depChunkPath {
		t.Fatalf("HashToGalaxyPath(%q) = %q, want %q", chunk.CompressedMD5, got, f.depChunkPath)
	}

	// 5 + 6. Secure link document, then templates ranked by priority.
	linkDoc, err := f.client.SecureLink(ctx, chainProductID, "/")
	if err != nil {
		t.Fatalf("SecureLink: %v", err)
	}
	templates, err := galaxy.CdnURLTemplatesFromJSON(linkDoc, []string{"cdnMain"})
	if err != nil {
		t.Fatalf("CdnURLTemplatesFromJSON: %v", err)
	}
	if len(templates) != 2 {
		t.Fatalf("templates = %v, want both endpoints", templates)
	}
	// The document lists cdnAlt first; the priority must move cdnMain ahead of
	// it, because the download path only ever uses templates[0].
	if !strings.HasPrefix(templates[0], "https://cdn.gog.com/chunks") {
		t.Fatalf("templates[0] = %q, want the endpoint the priority names", templates[0])
	}
	if !strings.Contains(templates[0], galaxy.GalaxyPathPlaceholder) {
		t.Fatalf("template = %q, want the galaxy path marker", templates[0])
	}

	// 7. The normal download path fills the marker with "/" + galaxy path.
	chunkURL := strings.ReplaceAll(templates[0], galaxy.GalaxyPathPlaceholder, "/"+f.depChunkPath)
	if strings.Contains(chunkURL, galaxy.GalaxyPathPlaceholder) {
		t.Errorf("chunk URL = %q, still carries the marker", chunkURL)
	}
	if want := "https://cdn.gog.com/chunks/" + f.depChunkPath; chunkURL != want {
		t.Errorf("chunk URL = %q, want %q", chunkURL, want)
	}

	// 8. The URL resolves to the chunk the manifest describes.
	body := f.fetch(t, chunkURL)
	if !bytes.Equal(body, chainChunk) {
		t.Errorf("chunk body = %q, want the fixture bytes", body)
	}
	if sum := md5.Sum(body); hex.EncodeToString(sum[:]) != chunk.CompressedMD5 {
		t.Errorf("downloaded md5 = %x, want %s", sum, chunk.CompressedMD5)
	}

	want := []string{
		"/products/" + chainProductID + "/os/windows/builds",
		f.buildManifestPath,
		f.depotManifestPath,
		"/products/" + chainProductID + "/secure_link",
		"/chunks/" + f.depChunkPath,
	}
	calls := f.calls()
	assertSequence(t, calls, want)
	for i, c := range calls[:4] {
		if c.Auth != "Bearer "+chainToken {
			t.Errorf("request %d (%s) Authorization = %q, want the bearer token", i, c.Path, c.Auth)
		}
	}
	// Only the keys that matter are asserted; the full query string is not, so
	// URL encoding details stay out of the test.
	if got := calls[0].Query.Get("generation"); got != "2" {
		t.Errorf("builds request generation = %q, want 2", got)
	}
	if got := calls[3].Query.Get("path"); got != "/" {
		t.Errorf("secure_link request path = %q, want /", got)
	}
}

// TestChainDependencyDownload covers the second use of the marker: a dependency
// chunk is fetched through the dependency manifest and the dependency link, and
// the marker is removed rather than filled in.
func TestChainDependencyDownload(t *testing.T) {
	f := newChainFixture(t)
	ctx := context.Background()

	depot := f.firstDepotEntry(t)
	depotItems, err := f.client.FilteredDepotItems(ctx, depot, "en", "64", galaxy.DepotOptions{IsDependency: true})
	if err != nil {
		t.Fatalf("FilteredDepotItems: %v", err)
	}
	if len(depotItems) != 1 {
		t.Fatalf("depot items = %d, want 1", len(depotItems))
	}
	if !depotItems[0].IsDependency {
		t.Error("IsDependency must be carried through the dependency manifest")
	}
	galaxyPath := galaxy.HashToGalaxyPath(depotItems[0].Chunks[0].CompressedMD5)

	linkDoc, err := f.client.DependencyLink(ctx, galaxyPath)
	if err != nil {
		t.Fatalf("DependencyLink: %v", err)
	}
	templates, err := galaxy.CdnURLTemplatesFromJSON(linkDoc, []string{"cdnMain"})
	if err != nil {
		t.Fatalf("CdnURLTemplatesFromJSON: %v", err)
	}
	if len(templates) != 1 {
		t.Fatalf("templates = %v, want one", templates)
	}
	chunkURL := strings.ReplaceAll(templates[0], galaxy.GalaxyPathPlaceholder, "")
	if strings.Contains(chunkURL, galaxy.GalaxyPathPlaceholder) {
		t.Errorf("dependency URL = %q, still carries the marker", chunkURL)
	}
	if want := "https://cdn.gog.com/chunks/" + f.depChunkPath; chunkURL != want {
		t.Errorf("dependency URL = %q, want %q", chunkURL, want)
	}
	if body := f.fetch(t, chunkURL); !bytes.Equal(body, chainChunk) {
		t.Errorf("chunk body = %q, want the fixture bytes", body)
	}

	want := []string{
		f.buildManifestPath,
		f.depotDepManifestPath,
		"/open_link",
		"/chunks/" + f.depChunkPath,
	}
	assertSequence(t, f.calls(), want)
}

// TestChainFilterStopsTheChain: a depot the caller's language and architecture do
// not select ends the chain as an empty result, with no side effect on the
// network — a selection failure is not a network failure.
func TestChainFilterStopsTheChain(t *testing.T) {
	f := newChainFixture(t, withMismatchedDepot())
	ctx := context.Background()

	depot := f.firstDepotEntry(t)
	depotItems, err := f.client.FilteredDepotItems(ctx, depot, "en", "64", galaxy.DepotOptions{})
	if err != nil {
		t.Fatalf("FilteredDepotItems: %v", err)
	}
	if len(depotItems) != 0 {
		t.Errorf("depot items = %d, want none", len(depotItems))
	}

	// Only the build manifest was asked for: no depot manifest, no link document,
	// no chunk.
	assertSequence(t, f.calls(), []string{f.buildManifestPath})
}

// TestChainDownlinkPathRecovery covers the other entry that resolves a download
// path: a link document's downlink URL. This is the single end-to-end-shaped
// case; the unit matrix for PathFromDownlinkURL lives in cdn_test.go.
func TestChainDownlinkPathRecovery(t *testing.T) {
	downlink := "https://cdn.gog.com/" + chainGameName + "/dir/file.bin?token=TTT"
	if got := galaxy.PathFromDownlinkURL(downlink, chainGameName); got != "/"+chainGameName+"/dir/file.bin" {
		t.Errorf("PathFromDownlinkURL(%q) = %q, want %q", downlink, got, "/"+chainGameName+"/dir/file.bin")
	}
}

// assertSequence fails unless the recorded requests are exactly the expected
// paths, in order.
func assertSequence(t *testing.T, calls []recorded, want []string) {
	t.Helper()
	if len(calls) != len(want) {
		t.Fatalf("requests = %d, want %d (%v)", len(calls), len(want), pathsOf(calls))
	}
	for i, path := range want {
		if calls[i].Path != path {
			t.Errorf("request %d = %q, want %q", i, calls[i].Path, path)
		}
	}
}

func pathsOf(calls []recorded) []string {
	paths := make([]string, 0, len(calls))
	for _, c := range calls {
		paths = append(paths, c.Path)
	}
	return paths
}

// quoteAll renders the fixture's string slices as a JSON array.
func quoteAll(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, v := range values {
		quoted = append(quoted, fmt.Sprintf("%q", v))
	}
	return strings.Join(quoted, ",")
}

// depotManifestBody is a depot manifest with one single-chunk entry. The chunk's
// md5 is the fixture's own; its uncompressed md5 and size are placeholders,
// because this test does not decompress anything.
func depotManifestBody(path, chunkMD5 string) string {
	return fmt.Sprintf(`{"depot":{"items":[{"path":%q,"chunks":[{"compressedMd5":%q,"md5":%q,`+
		`"compressedSize":%d,"size":%d}]}]}}`, path, chunkMD5, "uncompressed-md5-placeholder",
		len(chainChunk), len(chainChunk))
}

func mustArray(t *testing.T, v any, what string) []any {
	t.Helper()
	arr, ok := v.([]any)
	if !ok {
		t.Fatalf("%s = %#v, want an array", what, v)
	}
	return arr
}

func mustObject(t *testing.T, v any, what string) map[string]any {
	t.Helper()
	obj, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("%s = %#v, want an object", what, v)
	}
	return obj
}

func mustString(t *testing.T, v any, what string) string {
	t.Helper()
	s, ok := v.(string)
	if !ok {
		t.Fatalf("%s = %#v, want a string", what, v)
	}
	return s
}
