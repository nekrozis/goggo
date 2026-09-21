package core

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/nekrozis/goggo/internal/catalog"
	"github.com/nekrozis/goggo/internal/config"
	"github.com/nekrozis/goggo/internal/gamedetails"
	"github.com/nekrozis/goggo/internal/util"
)

// defaultInfoThreads is the default of the info-threads setting. The command
// tree carries only the surface that is actually supported, so the unregistered
// option has no front end to live in and sits with its only consumer.
const defaultInfoThreads = 4

// GameDetailsRequest is one acquisition run's input.
type GameDetailsRequest struct {
	// Products are the products to fetch: a numeric id passes through, anything
	// else is a game name looked up in the account's product list the way
	// RefMode asks, with the interactive selection a name gets elsewhere in
	// this package (selectProductID). An empty list is a caller error rather
	// than an empty answer: an empty result would read as "these products have
	// no files".
	Products []string

	// RefMode says how Products are read: by slug, or as the expression --regex
	// selects.
	RefMode ProductRefMode

	// InfoThreads is how many fetches run at once; zero means the run's
	// setting. It is NOT --threads — that one is the download concurrency, whose
	// default of eight does not carry over.
	InfoThreads int

	// Include overrides the run's type mask for this acquisition when set. Its
	// only producer is the download file chain, which forces "all" — a file
	// looked up by id must be findable whatever the mask says.
	Include *uint32
}

// GameDetails fetches and converts the download face of every requested
// product. It writes nothing to disk and reports no progress: the display
// belongs to whoever consumes the result.
//
// The answer is COMPLETE OR NOTHING. A failure anywhere — a name that matches
// no product, a request, the conversion — cancels the remaining workers and
// returns an error with no results, rather than a shorter list a caller could
// mistake for "this product has no files". Results are ordered by gamename, and
// an entry that is legitimately empty stays: it says "this product has no
// matching files", which is an answer.
func (d *Downloader) GameDetails(ctx context.Context, req GameDetailsRequest) ([]gamedetails.GameDetails, error) {
	if len(req.Products) == 0 {
		return nil, errors.New("galaxy: no products requested")
	}

	ids := make([]string, 0, len(req.Products))
	for _, product := range req.Products {
		id, notice, err := d.selectProductID(ctx, product, req.RefMode)
		if err != nil {
			return nil, err
		}
		if id == "" || notice.Text != "" {
			// A short work list would be the partial answer this function
			// refuses to hand back, so the message becomes the reason instead.
			text := notice.Text
			if text == "" {
				text = msgNoProducts
			}
			return nil, errors.New(text)
		}
		ids = append(ids, id)
	}

	include := d.effectiveInclude(req)
	owned, err := d.ownedGameIDs(ctx, include)
	if err != nil {
		return nil, err
	}

	workers := min(d.infoThreadCount(req.InfoThreads), len(ids))

	// One resolver for the whole run: it carries the credential refresh the
	// per-file resolution needs, and since it is shared, the pre-request
	// refresh below goes through the same one — a second refresher would be a
	// second lock and a second chance to refresh side by side. The owned set is
	// read-only once built, so the workers share it without a lock.
	resolver := d.gamedetailsResolver()

	results := make([]gamedetails.GameDetails, len(ids))
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		cursor  atomic.Int64
		once    sync.Once
		failure error
		wg      sync.WaitGroup
	)
	// Standard library only: no x/sync (unaudited, and this is a worker pool
	// plus a first-error latch). A worker takes the next index and writes its
	// own slot, so results need no lock; the first failure records itself and
	// cancels the rest.
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(cursor.Add(1)) - 1
				if i >= len(ids) || workCtx.Err() != nil {
					return
				}
				gd, err := d.gameDetailsFor(workCtx, ids[i], owned, resolver, include)
				if err != nil {
					once.Do(func() {
						failure = err
						cancel()
					})
					return
				}
				results[i] = gd
			}
		}()
	}
	wg.Wait()

	if failure != nil {
		return nil, failure
	}
	// A worker that stopped because the caller's context was cancelled reports
	// no failure of its own, so that reason is read from the context.
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	sort.SliceStable(results, func(i, j int) bool { return results[i].Gamename < results[j].Gamename })
	return results, nil
}

// ListGameDetails acquires the download face for `list details` / `list json`.
// An empty products list means the WHOLE account. Unlike a download there is no
// transfer behind it, so the implicit enumeration is allowed here and only
// here: acquisition reads, it never writes and never downloads.
func (d *Downloader) ListGameDetails(ctx context.Context, products []string, mode ProductRefMode) ([]gamedetails.GameDetails, error) {
	games, err := d.acquireForList(ctx, products, mode)
	if err != nil {
		return nil, err
	}
	// The filepaths are derived before answering: the text format's blacklist
	// filter compares against those destinations, so they must exist at display
	// time.
	for i := range games {
		games[i].MakeFilepaths(d.cfg.Directories)
	}
	return games, nil
}

func (d *Downloader) acquireForList(ctx context.Context, products []string, mode ProductRefMode) ([]gamedetails.GameDetails, error) {
	if len(products) > 0 {
		return d.GameDetails(ctx, GameDetailsRequest{Products: products, RefMode: mode})
	}
	// No products named: the whole account is listed, and the listing carries no
	// reference for a mode to read.
	res, err := catalog.List(ctx, d.web, d.accountListOptions())
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(res.Games))
	for _, g := range res.Games {
		ids = append(ids, g.ID)
	}
	if len(ids) == 0 {
		return nil, errors.New("galaxy: no products to list")
	}
	return d.GameDetails(ctx, GameDetailsRequest{Products: ids})
}

// infoThreadCount is the worker count of a run: the request's value, else the
// run's setting, else that setting's default. The last step exists because
// nothing writes the setting yet (the option is not registered), and zero
// workers would turn "no answer" into "an empty answer".
func (d *Downloader) infoThreadCount(requested int) int {
	if requested > 0 {
		return requested
	}
	if d.cfg.InfoThreads > 0 {
		return int(d.cfg.InfoThreads)
	}
	return defaultInfoThreads
}

// ownedGameIDs reads the account's owned product ids when the include mask asks
// for DLC content, and returns nil otherwise — nil means no filtering at all in
// the conversion (gamedetails.ProductInfoToGameDetails). The set is fetched
// explicitly rather than reused from a previous listing, so the filter behaves
// the same whether or not the run listed first; a failure to read it fails the
// run rather than reporting an account that owns nothing.
func (d *Downloader) ownedGameIDs(ctx context.Context, include uint32) (map[string]bool, error) {
	if include&config.GFDLC == 0 {
		return nil, nil
	}
	ids, err := d.web.OwnedGameIDs(ctx)
	if err != nil {
		return nil, err
	}
	owned := make(map[string]bool, len(ids))
	for _, id := range ids {
		owned[id] = true
	}
	return owned, nil
}

// gameDetailsFor is one worker's pass over one product.
//
// The refresh is idempotent, so a worker that arrives after another has
// refreshed pays nothing. A resolver failure is NOT a failure here: the
// resolver skips that one file and keeps converting
// (gamedetails.DownlinkResolver).
func (d *Downloader) gameDetailsFor(ctx context.Context, id string, owned map[string]bool, resolver *gamedetailsResolver, include uint32) (gamedetails.GameDetails, error) {
	if err := resolver.refresh.refreshIfExpired(ctx); err != nil {
		return gamedetails.GameDetails{}, fmt.Errorf("galaxy: refresh login: %w", err)
	}
	prodDoc, err := d.galaxy.Product(ctx, id)
	if err != nil {
		return gamedetails.GameDetails{}, err
	}
	cfg := d.cfg.DownloadConfig
	cfg.Include = include
	gd, err := gamedetails.ProductInfoToGameDetails(ctx, prodDoc.JSON, cfg, owned, resolver.Resolve)
	if err != nil {
		return gamedetails.GameDetails{}, err
	}
	gd.FilterWithPriorities(cfg.PlatformPriority, cfg.LanguagePriority)
	gd.FilterWithType(cfg.Include)

	// The save-* acquisition gate: the per-game details document is fetched
	// ONCE when any of its three consumers is asked for and still empty, and
	// never otherwise — with all flags off this whole branch issues no request,
	// which is the zero-extra-request regression contract. A failed fetch is
	// recorded, not swallowed; the acquisition itself continues.
	if (cfg.SaveSerials && gd.Serials == "") ||
		(cfg.SaveChangelogs && gd.Changelog == "") ||
		(cfg.SaveGameDetailsJSON && gd.GameDetailsJson == "") {
		details, err := d.web.GameDetailsJSON(ctx, id)
		if err != nil {
			gd.MetadataDiag = err.Error()
			return gd, nil
		}
		if cfg.SaveGameDetailsJSON && gd.GameDetailsJson == "" {
			rendered, err := util.StyledJSONBytes(details)
			if err != nil {
				gd.MetadataDiag = err.Error()
			} else {
				gd.GameDetailsJson = rendered
			}
		}
		if cfg.SaveSerials && gd.Serials == "" {
			gd.Serials, gd.SerialsDiag = serialsFromDetails(details)
		}
		if cfg.SaveChangelogs && gd.Changelog == "" {
			cl, err := gamedetails.ChangelogFromJSON(details)
			switch {
			case err != nil:
				gd.MetadataDiag = err.Error()
			case cl != "":
				gd.Changelog = cl
			}
		}
	}
	return gd, nil
}

// serialsFromDetails reads the cdKey member — a missing or null member is no
// serials, a wrong shape is a diagnostic — and hands the text to the
// extraction, whose fail-closed result travels as the second return value.
func serialsFromDetails(details []byte) (text, diag string) {
	var doc struct {
		CDKey jsontext.Value `json:"cdKey"`
	}
	if err := jsonv2.Unmarshal(details, &doc); err != nil {
		return "", "game details: " + err.Error()
	}
	cdKey, err := cdKeyString(doc.CDKey)
	if err != nil {
		return "", "game details: cdKey: " + err.Error()
	}
	text, unsupported := gamedetails.SerialsFromCDKey(cdKey)
	if unsupported {
		return "", "cdKey carries <span> markup this build does not parse: serials not written"
	}
	return text, ""
}

// cdKeyString reads the cdKey member. It is one of the loosely typed members: a
// number or a boolean is a value rather than a broken document, and a missing
// member and a null one are the empty string. Only a container has no text form.
//
// A number keeps the text a decode into a Go value produced for it — a float64
// rendered as fixed point, so 1e3 reads "1000" — rather than the spelling it
// arrived in, and a magnitude a float64 cannot hold is out of range, which is
// the boundary the typed decode drew as well. The document's own literal
// survives only where the raw bytes are handed on; this projection is not that
// place.
func cdKeyString(v jsontext.Value) (string, error) {
	switch v.Kind() {
	case jsontext.KindInvalid, jsontext.KindNull:
		return "", nil
	case jsontext.KindString, jsontext.KindNumber, jsontext.KindTrue, jsontext.KindFalse:
		tok, err := jsontext.NewDecoder(bytes.NewReader(v)).ReadToken()
		if err != nil {
			return "", err
		}
		if tok.Kind() == jsontext.KindNumber {
			f, err := tok.Float()
			if err != nil {
				return "", err
			}
			return strconv.FormatFloat(f, 'f', -1, 64), nil
		}
		return tok.String(), nil
	case jsontext.KindBeginObject:
		return "", errors.New("expected a string, got object")
	case jsontext.KindBeginArray:
		return "", errors.New("expected a string, got array")
	}
	return "", fmt.Errorf("expected a string, got %s", v.Kind())
}

// effectiveInclude is the type mask an acquisition run consumes: the request's
// override when it carries one, and the run's configured mask otherwise.
func (d *Downloader) effectiveInclude(req GameDetailsRequest) uint32 {
	if req.Include != nil {
		return *req.Include
	}
	return d.cfg.DownloadConfig.Include
}
