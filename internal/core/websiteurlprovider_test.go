package core

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/nekrozis/goggo/internal/auth"
	"github.com/nekrozis/goggo/internal/galaxy"
	"github.com/nekrozis/goggo/internal/httpx"
	"github.com/nekrozis/goggo/internal/model"
	"github.com/nekrozis/goggo/internal/transfer"
)

// providerFixture serves the downlink document and, when present, the checksum
// document, over a plain test server (the downlink URL is used verbatim).
type providerFixture struct {
	*httptest.Server

	mu     sync.Mutex
	bodies map[string]string
	hits   map[string]int
}

func newProviderFixture(t *testing.T) *providerFixture {
	t.Helper()
	f := &providerFixture{bodies: map[string]string{}, hits: map[string]int{}}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.hits[r.URL.Path]++
		body, ok := f.bodies[r.URL.Path]
		f.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(body))
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *providerFixture) set(path, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bodies[path] = body
}

func (f *providerFixture) hitCount(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits[path]
}

func (f *providerFixture) url(path string) string { return f.Server.URL + path }

// newProvider builds the provider with a controllable credential state: the
// expires counter says how many refresh calls still see an expired token.
func newProvider(t *testing.T, remoteXML bool, refreshes *atomic.Int32) *websiteURLProvider {
	t.Helper()
	return newProviderWithPolicy(t, remoteXML, refreshes, checksumGated)
}

// newProviderWithPolicy is newProvider with the checksum policy pinned: the
// batch chain runs the gated policy, the single-file chain the always policy.
func newProviderWithPolicy(t *testing.T, remoteXML bool, refreshes *atomic.Int32, policy checksumPolicy) *websiteURLProvider {
	t.Helper()
	hx, err := httpx.New(httpx.Config{UserAgent: "goggo-test/1.0"})
	if err != nil {
		t.Fatalf("httpx.New: %v", err)
	}
	store, err := auth.Open("")
	if err != nil {
		t.Fatalf("auth.Open: %v", err)
	}
	gx, err := galaxy.New(hx, store)
	if err != nil {
		t.Fatalf("galaxy.New: %v", err)
	}
	// The credential state is real: the token starts expired and the refresh
	// flips it, so the in-lock re-check has something to catch.
	var expiredFlag atomic.Bool
	expiredFlag.Store(true)
	return &websiteURLProvider{
		galaxy:    gx,
		remoteXML: remoteXML,
		policy:    policy,
		refresh: tokenRefresher{
			refresh: func(ctx context.Context) error {
				refreshes.Add(1)
				expiredFlag.Store(false)
				return nil
			},
			expired: func() bool { return expiredFlag.Load() },
		},
	}
}

// TestWebsiteURLProviderResolve locks the document walk: the downlink member is
// the download url, the checksum document is fetched only for checksummed files
// when remote XML is on, and the two unusable-document shapes map onto the
// transfer sentinels.
func TestWebsiteURLProviderResolve(t *testing.T) {
	const downlinkDoc = `{"downlink":"https://cdn.example.com/file.bin","checksum":"%s/checksum"}`
	const checksumDoc = `<file name="setup.bin" md5="abc" total_size="12"/>`

	f := newProviderFixture(t)
	f.set("/downlink", fmt.Sprintf(downlinkDoc, f.URL))
	f.set("/checksum", checksumDoc)

	refreshes := &atomic.Int32{}
	p := newProvider(t, true, refreshes)
	task := model.WebsiteTask{Destination: "setup.bin", DownlinkURL: f.url("/downlink"), Gamename: "game", Checksummed: true}

	url, checksumXML, err := p.Resolve(context.Background(), task)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if url != "https://cdn.example.com/file.bin" {
		t.Errorf("url = %q", url)
	}
	if checksumXML != checksumDoc {
		t.Errorf("checksum xml = %q, want the served document", checksumXML)
	}
	if hits := f.hitCount("/checksum"); hits != 1 {
		t.Errorf("checksum requests = %d, want 1", hits)
	}

	// A non-checksummed file must not touch the checksum document.
	f2 := newProviderFixture(t)
	f2.set("/downlink", fmt.Sprintf(downlinkDoc, f2.URL))
	p2 := newProvider(t, true, refreshes)
	if _, _, err := p2.Resolve(context.Background(), model.WebsiteTask{
		Destination: "extra.bin", DownlinkURL: f2.url("/downlink"),
	}); err != nil {
		t.Fatalf("Resolve (extra): %v", err)
	}
	if hits := f2.hitCount("/checksum"); hits != 0 {
		t.Errorf("checksum requests = %d, want none for a non-checksummed file", hits)
	}

	// An empty document and a document without a downlink member map onto the
	// transfer sentinels the worker skips on.
	f3 := newProviderFixture(t)
	f3.set("/downlink", `{}`)
	p3 := newProvider(t, true, refreshes)
	_, _, err = p3.Resolve(context.Background(), model.WebsiteTask{DownlinkURL: f3.url("/downlink")})
	if !errors.Is(err, transfer.ErrEmptyDownlink) {
		t.Errorf("empty document err = %v, want ErrEmptyDownlink", err)
	}

	f4 := newProviderFixture(t)
	f4.set("/downlink", `{"other":1}`)
	p4 := newProvider(t, true, refreshes)
	_, _, err = p4.Resolve(context.Background(), model.WebsiteTask{DownlinkURL: f4.url("/downlink")})
	if !errors.Is(err, transfer.ErrNoDownlink) {
		t.Errorf("no downlink err = %v, want ErrNoDownlink", err)
	}
}

// TestWebsiteURLProviderConcurrentRefresh locks the concurrent-refresh rule on
// the website path too: workers that all see an expired token produce exactly
// one refresh, not one per worker.
func TestWebsiteURLProviderConcurrentRefresh(t *testing.T) {
	f := newProviderFixture(t)
	f.set("/downlink", `{"downlink":"https://cdn.example.com/file.bin"}`)

	refreshes := &atomic.Int32{}
	p := newProvider(t, false, refreshes)

	const workers = 8
	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			if _, _, err := p.Resolve(context.Background(), model.WebsiteTask{
				Destination: "file.bin", DownlinkURL: f.url("/downlink"),
			}); err != nil {
				t.Errorf("Resolve: %v", err)
			}
		}()
	}
	wg.Wait()

	if got := refreshes.Load(); got != 1 {
		t.Errorf("refreshes = %d, want exactly 1 across %d concurrent resolves", got, workers)
	}
}
