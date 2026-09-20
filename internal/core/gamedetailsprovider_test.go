package core

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/nekrozis/goggo/internal/config"
	"github.com/nekrozis/goggo/internal/galaxy"
	"github.com/nekrozis/goggo/internal/gamedetails"
	"github.com/nekrozis/goggo/internal/httpx"
)

// The resolver is what the conversion's seam needs, so the assertion that
// matters most is the compile-time one; keeping it here documents where the
// contract lives.
var _ gamedetails.DownlinkResolver = (*gamedetailsResolver)(nil).Resolve

func newGamedetailsResolver(t *testing.T, f *providerFixture, refreshes *atomic.Int32) *gamedetailsResolver {
	t.Helper()
	hx, err := httpx.New(httpx.Config{UserAgent: "goggo-test/1.0"})
	if err != nil {
		t.Fatalf("httpx.New: %v", err)
	}
	gx, err := galaxy.New(hx, config.NewGalaxyConfig())
	if err != nil {
		t.Fatalf("galaxy.New: %v", err)
	}
	return &gamedetailsResolver{
		galaxy: gx,
		refresh: tokenRefresher{
			refresh: func(context.Context) error {
				refreshes.Add(1)
				return nil
			},
			expired: func() bool { return true },
		},
	}
}

// TestGamedetailsResolverResolve locks the happy path: the document's downlink
// member is the source url, and the persistent path is derived by
// galaxy.PathFromDownlinkURL — computed side by side here, so a second
// implementation of the url-to-path rule would show up as a difference.
func TestGamedetailsResolverResolve(t *testing.T) {
	const downlink = "https://cdn.example.com/games/game%20name/setup.exe?token=abc"
	f := newProviderFixture(t)
	f.set("/dlc", `{"downlink":"`+downlink+`"}`)

	var refreshes atomic.Int32
	r := newGamedetailsResolver(t, f, &refreshes)

	got, err := r.Resolve(context.Background(), "gamename", f.url("/dlc"))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.URL != downlink {
		t.Errorf("URL = %q, want the document's downlink %q", got.URL, downlink)
	}
	if want := galaxy.PathFromDownlinkURL(downlink, "gamename"); got.Path != want {
		t.Errorf("Path = %q, want %q", got.Path, want)
	}
	if got := refreshes.Load(); got != 1 {
		t.Errorf("refreshes = %d, want the expired credentials refreshed once before the fetch", got)
	}
	if f.hitCount("/dlc") != 1 {
		t.Errorf("downlink document fetched %d times, want once", f.hitCount("/dlc"))
	}
}

// TestGamedetailsResolverRejectsMalformedDownlinks locks the three failures
// apart, and that none of them hands back a partial result: a resolver error
// makes the conversion skip the file, so what it returns alongside must be
// unusable rather than half-true.
func TestGamedetailsResolverRejectsMalformedDownlinks(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want error
	}{
		{"empty document", `{}`, errEmptyDownlinkDoc},
		{"no downlink", `{"other":"x"}`, errNoDownlink},
		{"null downlink", `{"downlink":null}`, errNoDownlink},
		{"downlink is a number", `{"downlink":42}`, errDownlinkNotString},
		{"downlink is an object", `{"downlink":{"url":"x"}}`, errDownlinkNotString},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newProviderFixture(t)
			f.set("/dlc", tc.body)

			var refreshes atomic.Int32
			got, err := newGamedetailsResolver(t, f, &refreshes).Resolve(context.Background(), "gamename", f.url("/dlc"))
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
			if got != (gamedetails.ResolvedFile{}) {
				t.Errorf("result = %+v, want the zero value", got)
			}
		})
	}
}
