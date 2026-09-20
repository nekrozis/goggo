package catalog

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/nekrozis/goggo/internal/config"
	"github.com/nekrozis/goggo/internal/webapi"
)

// pageCall records one FilteredProductsPage invocation.
type pageCall struct {
	query webapi.ProductQuery
	page  int
}

// fakeFetcher is a scripted ProductFetcher. It consumes pages in order, so each
// listing run needs its own instance.
type fakeFetcher struct {
	pages      []webapi.ProductPage
	pageCalls  []pageCall
	pageErr    error
	owned      []string
	ownedErr   error
	details    map[string]map[string]any
	detailsErr map[string]error
	detailIDs  []string
}

func (f *fakeFetcher) FilteredProductsPage(_ context.Context, q webapi.ProductQuery, page int) (webapi.ProductPage, error) {
	f.pageCalls = append(f.pageCalls, pageCall{query: q, page: page})
	if f.pageErr != nil {
		return webapi.ProductPage{}, f.pageErr
	}
	if len(f.pages) == 0 {
		return webapi.ProductPage{Page: page, TotalPages: 0}, nil
	}
	pg := f.pages[0]
	f.pages = f.pages[1:]
	return pg, nil
}

func (f *fakeFetcher) GameDetailsJSON(_ context.Context, gameID string) (map[string]any, error) {
	f.detailIDs = append(f.detailIDs, gameID)
	if err, ok := f.detailsErr[gameID]; ok {
		return nil, err
	}
	return f.details[gameID], nil
}

func (f *fakeFetcher) OwnedGameIDs(context.Context) ([]string, error) {
	return f.owned, f.ownedErr
}

// product builds a raw product map; extra merges additional members.
func product(slug string, id any, extra map[string]any) map[string]any {
	p := map[string]any{"slug": slug, "id": id}
	for k, v := range extra {
		p[k] = v
	}
	return p
}

// onePage wraps products in a single terminal page.
func onePage(products ...map[string]any) []webapi.ProductPage {
	return []webapi.ProductPage{{Page: 1, TotalPages: 1, Products: products}}
}

func list(t *testing.T, ff *fakeFetcher, opts ListOptions) ListResult {
	t.Helper()
	res, err := List(context.Background(), ff, opts)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	return res
}

func TestListMapsProductsAndReturnsOwnedIDs(t *testing.T) {
	ff := &fakeFetcher{
		owned: []string{"7", "8"},
		pages: onePage(
			product("alpha", float64(1207659156), map[string]any{
				"isNew":   true,
				"updates": float64(3),
				"worksOn": map[string]any{"Windows": true, "Mac": false, "Linux": true},
			}),
			product("beta", "beta-id", map[string]any{"updates": nil}),
		),
	}

	res := list(t, ff, ListOptions{})
	if len(res.Games) != 2 {
		t.Fatalf("games = %d, want 2", len(res.Games))
	}
	alpha := res.Games[0]
	if alpha.Name != "alpha" || alpha.ID != "1207659156" || !alpha.IsNew || alpha.Updates != 3 {
		t.Errorf("alpha = %+v", alpha)
	}
	beta := res.Games[1]
	if beta.Name != "beta" || beta.ID != "beta-id" || beta.IsNew || beta.Updates != 0 {
		t.Errorf("beta = %+v", beta)
	}
	if len(res.OwnedIDs) != 2 || res.OwnedIDs[0] != "7" {
		t.Errorf("owned ids = %v", res.OwnedIDs)
	}
}

// TestListProductIDShapes locks the integer-shaped stringification rule.
//
// The boolean entries pin the shape gate: a boolean must stringify
// ("true"/"false") instead of falling into jsonval.Int, which would coerce true
// to "1". Do not widen that gate.
func TestListProductIDShapes(t *testing.T) {
	ff := &fakeFetcher{pages: onePage(
		product("int", float64(12), nil),
		product("string", "abc", nil),
		product("real", float64(12.5), nil),
		product("missing", nil, nil),
		product("bool true", true, nil),
		product("bool false", false, nil),
	)}

	res := list(t, ff, ListOptions{})
	for i, want := range []string{"12", "abc", "12.5", "", "true", "false"} {
		if res.Games[i].ID != want {
			t.Errorf("id[%d] = %q, want %q", i, res.Games[i].ID, want)
		}
	}
}

// TestListProductUpdatesShapes locks the parsing of the updates member. The
// boolean entry pins the same shape gate as productID: a boolean goes through the
// string parser and yields 0, not 1.
func TestListProductUpdatesShapes(t *testing.T) {
	cases := []struct {
		name string
		set  bool
		in   any
		want int
	}{
		{"absent", false, nil, 0},
		{"null", true, nil, 0},
		{"integer", true, float64(4), 4},
		{"numeric string", true, "5", 5},
		{"string with suffix", true, "12abc", 12},
		{"real", true, float64(7.9), 7},
		{"not a number", true, "abc", 0},
		{"empty string", true, "", 0},
		{"negative", true, "-3", -3},
		{"huge string", true, "99999999999999999999", 0},
		{"boolean", true, true, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			extra := map[string]any{}
			if c.set {
				extra["updates"] = c.in
			}
			ff := &fakeFetcher{pages: onePage(product("g", float64(1), extra))}
			if got := list(t, ff, ListOptions{}).Games[0].Updates; got != c.want {
				t.Errorf("updates = %d, want %d", got, c.want)
			}
		})
	}
}

// TestListPlatformDetection covers the worksOn mask and the "no platform means
// all platforms" fallback.
func TestListPlatformDetection(t *testing.T) {
	products := func() []webapi.ProductPage {
		return onePage(
			product("windows-only", float64(1), map[string]any{"worksOn": map[string]any{"Windows": true}}),
			product("no-platform", float64(2), map[string]any{"worksOn": map[string]any{}}),
			product("linux", float64(3), map[string]any{"worksOn": map[string]any{"Linux": true}}),
		)
	}

	res := list(t, &fakeFetcher{pages: products()},
		ListOptions{PlatformDetection: true, InstallerPlatform: config.PlatformLinux})
	if len(res.Games) != 2 || res.Games[0].Name != "no-platform" || res.Games[1].Name != "linux" {
		t.Errorf("detected games = %+v", res.Games)
	}

	all := list(t, &fakeFetcher{pages: products()}, ListOptions{})
	if len(all.Games) != 3 {
		t.Errorf("without detection games = %d, want 3", len(all.Games))
	}
}

func TestListNewOnlyAndGameFilters(t *testing.T) {
	pages := func() []webapi.ProductPage {
		return onePage(
			product("alpha-one", float64(1), map[string]any{"isNew": true}),
			product("alpha-two", float64(2), map[string]any{"isNew": false}),
			product("beta", float64(3), map[string]any{"isNew": false}),
		)
	}

	if got := list(t, &fakeFetcher{pages: pages()}, ListOptions{NewOnly: true}).Games; len(got) != 1 || got[0].Name != "alpha-one" {
		t.Errorf("NewOnly games = %+v", got)
	}
	if got := list(t, &fakeFetcher{pages: pages()}, ListOptions{GameRegex: "alpha"}).Games; len(got) != 2 {
		t.Errorf("GameRegex games = %+v", got)
	}
	// A pattern is a substring match, not anchored.
	if got := list(t, &fakeFetcher{pages: pages()}, ListOptions{GameRegex: "^alpha$"}).Games; len(got) != 0 {
		t.Errorf("anchored GameRegex games = %+v, want none", got)
	}
}

// TestListHiddenPass walks the two-round pagination of the hidden-products pass.
func TestListHiddenPass(t *testing.T) {
	ff := &fakeFetcher{pages: []webapi.ProductPage{
		{Page: 1, TotalPages: 1, Products: []map[string]any{product("zeta", float64(1), nil)}},
		{Page: 1, TotalPages: 2, Products: []map[string]any{product("beta", float64(2), nil)}},
		{Page: 2, TotalPages: 2, Products: []map[string]any{product("alpha", float64(3), nil)}},
	}}
	res := list(t, ff, ListOptions{IncludeHidden: true, Updated: true})

	if len(res.Games) != 3 {
		t.Fatalf("games = %+v, want 3", res.Games)
	}
	// IncludeHidden sorts by name.
	for i, want := range []string{"alpha", "beta", "zeta"} {
		if res.Games[i].Name != want {
			t.Errorf("games[%d] = %q, want %q", i, res.Games[i].Name, want)
		}
	}
	want := []pageCall{
		{query: webapi.ProductQuery{HiddenFlag: 0, IsUpdated: 1}, page: 1},
		{query: webapi.ProductQuery{HiddenFlag: 1, IsUpdated: 1}, page: 1},
		{query: webapi.ProductQuery{HiddenFlag: 1, IsUpdated: 1}, page: 2},
	}
	if len(ff.pageCalls) != len(want) {
		t.Fatalf("page calls = %+v, want %+v", ff.pageCalls, want)
	}
	for i := range want {
		got := ff.pageCalls[i]
		// ProductQuery holds a slice, so the calls are compared field-wise.
		if got.page != want[i].page ||
			got.query.HiddenFlag != want[i].query.HiddenFlag ||
			got.query.IsUpdated != want[i].query.IsUpdated ||
			len(got.query.Tags) != 0 {
			t.Errorf("page call %d = %+v, want %+v", i, got, want[i])
		}
	}
}

func TestListTotalPagesZeroIsEmpty(t *testing.T) {
	ff := &fakeFetcher{pages: []webapi.ProductPage{{Page: 1, TotalPages: 0}}}
	res := list(t, ff, ListOptions{})
	if len(res.Games) != 0 {
		t.Errorf("games = %+v, want none", res.Games)
	}
	if len(ff.pageCalls) != 1 {
		t.Errorf("page calls = %d, want 1", len(ff.pageCalls))
	}
}

// TestListPropagatesOwnedError locks that a failing owned-ids fetch fails the
// listing instead of looking like an account without games.
func TestListPropagatesOwnedError(t *testing.T) {
	boom := errors.New("boom")
	ff := &fakeFetcher{ownedErr: boom}
	if _, err := List(context.Background(), ff, ListOptions{}); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if len(ff.pageCalls) != 0 {
		t.Errorf("page calls = %+v, want none (fail before fetching products)", ff.pageCalls)
	}
}

func TestListPropagatesPageError(t *testing.T) {
	boom := errors.New("page boom")
	ff := &fakeFetcher{pageErr: boom}
	if _, err := List(context.Background(), ff, ListOptions{}); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want page boom", err)
	}
}

// TestListInvalidRegexFailsBeforeFetching locks that an invalid pattern fails the
// listing before any request is made.
func TestListInvalidRegexFailsBeforeFetching(t *testing.T) {
	ff := &fakeFetcher{}
	if _, err := List(context.Background(), ff, ListOptions{GameRegex: "("}); err == nil {
		t.Fatal("List: want error for an invalid --game-regex")
	}
	if len(ff.pageCalls) != 0 || ff.detailIDs != nil || ff.owned != nil {
		t.Errorf("fetcher was used before validation: %+v", ff.pageCalls)
	}
}

// TestListDLCEnrichment covers the two triggers, the GFDLC gate and the
// soft-skip behaviour.
func TestListDLCEnrichment(t *testing.T) {
	details := map[string]map[string]any{
		"1": {"dlcs": []any{
			map[string]any{"manualUrl": "https://www.gog.com/downloads/dlc_one/x"},
			map[string]any{"manualUrl": "https://www.gog.com/downloads/dlc_two/y"},
			map[string]any{"manualUrl": "https://www.gog.com/downloads/dlc_one/z"},
		}},
	}

	t.Run("dlcCount triggers", func(t *testing.T) {
		ff := &fakeFetcher{
			pages:   onePage(product("g", float64(1), map[string]any{"dlcCount": float64(2)})),
			details: details,
		}
		got := list(t, ff, ListOptions{Include: config.GFDLC}).Games[0]
		if got.GameDetailsJSON == nil {
			t.Error("details not stored")
		}
		if len(got.DLCNames) != 2 || got.DLCNames[0] != "dlc_one" || got.DLCNames[1] != "dlc_two" {
			t.Errorf("dlc names = %v", got.DLCNames)
		}
	})

	t.Run("ignore regex triggers", func(t *testing.T) {
		ff := &fakeFetcher{
			pages:   onePage(product("special-game", float64(1), nil)),
			details: details,
		}
		res := list(t, ff, ListOptions{Include: config.GFDLC, IgnoreDLCCountRE: "special"})
		if len(ff.detailIDs) != 1 || res.Games[0].DLCNames == nil {
			t.Errorf("details calls = %v, names = %v", ff.detailIDs, res.Games[0].DLCNames)
		}
	})

	t.Run("no trigger", func(t *testing.T) {
		ff := &fakeFetcher{pages: onePage(product("plain", float64(1), nil)), details: details}
		list(t, ff, ListOptions{Include: config.GFDLC})
		if len(ff.detailIDs) != 0 {
			t.Errorf("details fetched without a trigger: %v", ff.detailIDs)
		}
	})

	t.Run("include without GFDLC", func(t *testing.T) {
		ff := &fakeFetcher{
			pages:   onePage(product("g", float64(1), map[string]any{"dlcCount": float64(1)})),
			details: details,
		}
		list(t, ff, ListOptions{Include: config.GFBase})
		if len(ff.detailIDs) != 0 {
			t.Errorf("details fetched without GFDLC: %v", ff.detailIDs)
		}
	})

	t.Run("details failure is a soft skip", func(t *testing.T) {
		ff := &fakeFetcher{
			pages:      onePage(product("g", float64(1), map[string]any{"dlcCount": float64(1)})),
			detailsErr: map[string]error{"1": errors.New("details boom")},
		}
		res := list(t, ff, ListOptions{Include: config.GFDLC})
		if len(res.Games) != 1 {
			t.Fatalf("games = %+v, want the game to stay listed", res.Games)
		}
		if res.Games[0].GameDetailsJSON != nil || res.Games[0].DLCNames != nil {
			t.Errorf("game = %+v, want empty DLC information", res.Games[0])
		}
	})
}

// TestListFilterListFile drives LoadFilterList through List.
func TestListFilterListFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "filters.txt")
	if err := os.WriteFile(path, []byte("alpha\n\nbeta\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ff := &fakeFetcher{pages: onePage(
		product("alpha-one", float64(1), nil),
		product("gamma", float64(2), nil),
	)}

	res := list(t, ff, ListOptions{FilterListPath: path})
	if len(res.Games) != 1 || res.Games[0].Name != "alpha-one" {
		t.Errorf("games = %+v, want only alpha-one", res.Games)
	}

	missing := filepath.Join(t.TempDir(), "missing.txt")
	if _, err := List(context.Background(), &fakeFetcher{}, ListOptions{FilterListPath: missing}); err == nil {
		t.Error("missing filter list: want error")
	}
}
