package core

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/nekrozis/goggo/internal/catalog"
	"github.com/nekrozis/goggo/internal/config"
	"github.com/nekrozis/goggo/internal/jsonval"
	"github.com/nekrozis/goggo/internal/model"
)

// Messages printed for these two commands instead of doing the work. They are
// messages, not failures:
const (
	msgNoProducts        = "Didn't match any products"
	msgNoSelection       = "Unable to read selection"
	msgNoLinuxSupport    = "Galaxy API doesn't have Linux support"
	msgCheckInstallers   = "Checking for installers that can be used as repository"
	msgGenerationsOneTwo = "Only generation 1 and 2 builds are supported currently"
	msgGenerationTwoOnly = "Only generation 2 builds are supported currently"
)

// errInstallerFallback is returned after the two Linux messages: the installer
// fallback is not implemented in this build, and printing the two lines and
// exiting as though it had run would report work that never happened.
var errInstallerFallback = errors.New(
	"the installer fallback for a platform without Galaxy builds is not implemented in this build")

// numericIDRE matches a product-id argument in its numeric form.
var numericIDRE = regexp.MustCompile(`^[0-9]+$`)

// ProductRefMode says how a product reference is read.
type ProductRefMode uint8

const (
	// ProductRefExact matches the reference against the account's product slugs
	// by whole-string equality, ignoring case. It is what a name printed by
	// "list games" resolves to.
	ProductRefExact ProductRefMode = iota

	// ProductRefRegex matches it as an unanchored regular expression, the way
	// the listing filter does.
	ProductRefRegex
)

// Notice is a message printed instead of doing the work. It is not an error:
// the exit code stays 0. Err selects the stream — the support and generation
// messages go to stdout, the argument-resolution failures to stderr.
type Notice struct {
	Text string
	Err  bool
}

// BuildRow is one line of the build listing printed when no build is selected.
type BuildRow struct {
	VersionName   string
	DatePublished string
	BuildID       string

	Generation int
	Index      int
}

// BuildsResult is what ShowBuilds produces. At most one of the three fields is
// set: Builds carries the listing when no build was selected, Manifest the
// document of the selected build, and Notice the message printed when the
// command stops early.
//
// Notice and an error can come back together: the Linux fallback prints its two
// lines and then reports that this build cannot continue, so the front end
// renders the result first and the error second.
type BuildsResult struct {
	Builds   []BuildRow
	Manifest map[string]any
	Notice   Notice
}

// CDNsResult is what ListCDNs produces: the endpoint names and, when the
// command stops early, the message printed instead.
type CDNsResult struct {
	Names  []string
	Notice Notice
}

// ShowBuilds resolves the product id, reads its build list, sorts it the way
// --galaxy-builds-sort asks, then either returns the listing or fetches and
// returns the manifest of the selected build.
func (d *Downloader) ShowBuilds(ctx context.Context, productID, buildID string, mode ProductRefMode) (BuildsResult, error) {
	id, notice, err := d.selectProductID(ctx, productID, mode)
	if err != nil {
		return BuildsResult{}, err
	}
	if notice.Text != "" {
		return BuildsResult{Notice: notice}, nil
	}
	if id == "" {
		// An empty resolved id means there is no work to do.
		return BuildsResult{}, nil
	}
	return d.showBuildsFor(ctx, id, buildID)
}

// ListCDNs resolves the product id, reads the build list, and returns the CDN
// endpoint names of the secure link of the selected build.
func (d *Downloader) ListCDNs(ctx context.Context, productID, buildID string, mode ProductRefMode) (CDNsResult, error) {
	id, notice, err := d.selectProductID(ctx, productID, mode)
	if err != nil {
		return CDNsResult{}, err
	}
	if notice.Text != "" {
		return CDNsResult{Notice: notice}, nil
	}
	if id == "" {
		return CDNsResult{}, nil
	}

	platform := platformName(d.cfg.DownloadConfig.GalaxyPlatform)
	doc, err := d.galaxy.ProductBuilds(ctx, id, platform, "")
	if err != nil {
		return CDNsResult{}, err
	}
	if doc, err = d.sortProductBuilds(doc); err != nil {
		return CDNsResult{}, err
	}

	if len(doc) == 0 && platform == platformLinux {
		return CDNsResult{Notice: Notice{Text: msgNoLinuxSupport}}, nil
	}

	items, err := buildItems(doc)
	if err != nil {
		return CDNsResult{}, err
	}
	index, err := buildIndexFor(items, buildID)
	if err != nil {
		return CDNsResult{}, err
	}
	if index < 0 {
		index = 0
	}
	generation, _, err := buildEntry(items, index)
	if err != nil {
		return CDNsResult{}, err
	}
	if generation != 2 {
		return CDNsResult{Notice: Notice{Text: msgGenerationTwoOnly}}, nil
	}

	link, err := d.galaxy.SecureLink(ctx, id, "/")
	if err != nil {
		return CDNsResult{}, err
	}
	names, err := endpointNames(link)
	if err != nil {
		return CDNsResult{}, err
	}
	return CDNsResult{Names: names}, nil
}

// showBuildsFor is the by-id half of ShowBuilds: it reads the build list and
// returns either the listing or the manifest of the selected build.
func (d *Downloader) showBuildsFor(ctx context.Context, productID, buildID string) (BuildsResult, error) {
	platform := platformName(d.cfg.DownloadConfig.GalaxyPlatform)
	doc, err := d.galaxy.ProductBuilds(ctx, productID, platform, "")
	if err != nil {
		return BuildsResult{}, err
	}
	if doc, err = d.sortProductBuilds(doc); err != nil {
		return BuildsResult{}, err
	}

	// An empty answer for Linux means the API has no Linux support. An HTTP
	// failure does not land here: it is a *httpx.StatusError, not an empty
	// document.
	if len(doc) == 0 && platform == platformLinux {
		return BuildsResult{
			Notice: Notice{Text: msgNoLinuxSupport + "\n" + msgCheckInstallers},
		}, errInstallerFallback
	}

	items, err := buildItems(doc)
	if err != nil {
		return BuildsResult{}, err
	}
	index, err := buildIndexFor(items, buildID)
	if err != nil {
		return BuildsResult{}, err
	}

	if index < 0 {
		rows := make([]BuildRow, 0, len(items))
		for i, raw := range items {
			item, err := jsonval.Object(raw)
			if err != nil {
				return BuildsResult{}, fmt.Errorf("galaxy: builds items[%d]: %w", i, err)
			}
			row, err := buildRow(i, item)
			if err != nil {
				return BuildsResult{}, err
			}
			rows = append(rows, row)
		}
		return BuildsResult{Builds: rows}, nil
	}

	generation, link, err := buildEntry(items, index)
	if err != nil {
		return BuildsResult{}, err
	}
	switch generation {
	case 1:
		manifest, err := d.galaxy.ManifestV1(ctx, link)
		if err != nil {
			return BuildsResult{}, err
		}
		return BuildsResult{Manifest: manifest}, nil
	case 2:
		manifest, err := d.galaxy.ManifestV2(ctx, hashFromLink(link), false)
		if err != nil {
			return BuildsResult{}, err
		}
		return BuildsResult{Manifest: manifest}, nil
	default:
		return BuildsResult{Notice: Notice{Text: msgGenerationsOneTwo}}, nil
	}
}

// selectProductID resolves the product argument: a numeric argument IS the
// product id; otherwise the account's product list is searched the way mode
// asks — by slug equality, or by the unanchored expression --regex selects —
// with an interactive selection when more than one product matches.
//
// The outcome is one of three, and the two kinds of failure are kept apart:
//
//	an id            + an empty Notice + no error   — resolved
//	nothing to name  + a Notice      + no error     — "there is no such product"
//	nothing to name  + an empty Notice + an error   — the command could not finish
//
// The split matters because a Notice is not a failure: a caller that only shows
// something treats "there is nothing to show" as its answer. A reference that
// matched several products and could not be chosen is not that — the objects
// exist and the selection failed — so it travels as an error and every caller
// reports a failed run.
func (d *Downloader) selectProductID(ctx context.Context, ref string, mode ProductRefMode) (string, Notice, error) {
	if numericIDRE.MatchString(ref) {
		return ref, Notice{}, nil
	}

	res, err := catalog.List(ctx, d.web, d.gameListOptions(ref, mode))
	if err != nil {
		return "", Notice{}, err
	}
	if mode == ProductRefExact {
		// An exact read is a whole-string comparison, so it is done here on the
		// slugs the listing returned rather than by handing the reference to the
		// listing filter, whose match is a substring one.
		matched := make([]model.GameItem, 0, 1)
		for _, g := range res.Games {
			if strings.EqualFold(g.Name, ref) {
				matched = append(matched, g)
			}
		}
		res.Games = matched
	}
	switch len(res.Games) {
	case 0:
		return "", Notice{Text: noProductMessage(ref, mode), Err: true}, nil
	case 1:
		return res.Games[0].ID, Notice{}, nil
	}

	names := make([]string, 0, len(res.Games))
	for _, g := range res.Games {
		names = append(names, g.Name)
	}
	index, err := d.ui.SelectProduct(names)
	if err != nil || index < 0 || index >= len(names) {
		// An unanswerable prompt and a console that hands back an unusable index
		// are one outcome: no selection was made. The products are there, so this
		// is a failed command rather than an empty answer — returning it as a
		// notice would let a show command report success over a choice it never
		// made.
		return "", Notice{}, errors.New(msgNoSelection)
	}
	return res.Games[index].ID, Notice{}, nil
}

// noProductMessage is what an unresolved reference reports. An exact read names
// the reference the user typed and says where the exact spelling comes from,
// because a typo is the failure it has to explain; the expression form keeps
// the listing's own wording.
func noProductMessage(ref string, mode ProductRefMode) string {
	if mode == ProductRefRegex {
		return msgNoProducts
	}
	return fmt.Sprintf("no product named %q (list games prints exact names; --regex matches a pattern)", ref)
}

// gameListOptions assembles the product query for a reference lookup.
//
// It duplicates the assembly in internal/cli/list.go on purpose: the listing
// command still lives there, and the two copies converge when it moves into
// this package. Keeping them separate keeps this step from changing listing
// behaviour.
//
// The reference is a product the user named, so it outranks the configured
// filter list, which is the listing filter's own precedence. Only the expression
// form is handed to the listing filter; the exact form is compared against the
// slugs the listing returns.
func (d *Downloader) gameListOptions(ref string, mode ProductRefMode) catalog.ListOptions {
	opts := d.accountListOptions()
	opts.FilterListPath = ""
	if mode == ProductRefRegex {
		opts.GameRegex = ref
	}
	return opts
}

// accountListOptions assembles the query for a whole-account listing: no
// reference, so the configured filter list decides what the listing contains,
// exactly as it does for a plain listing.
func (d *Downloader) accountListOptions() catalog.ListOptions {
	cfg := d.cfg
	return catalog.ListOptions{
		Tags:              cfg.DownloadConfig.Tags,
		FilterListPath:    cfg.GameListFilePath,
		IgnoreDLCCountRE:  cfg.IgnoreDLCCountRegex,
		InstallerPlatform: cfg.DownloadConfig.InstallerPlatform,
		Include:           cfg.DownloadConfig.Include,
		Updated:           cfg.Updated,
		NewOnly:           cfg.New,
		IncludeHidden:     cfg.IncludeHiddenProducts,
		PlatformDetection: cfg.PlatformDetection,
		UpdateCache:       cfg.UpdateCache,
	}
}

// sortProductBuilds applies the requested order to the build list. An empty,
// "none" or unknown order leaves the document alone.
//
// The sort is stable, so entries with equal keys keep the document order. That
// matters here: equal keys are what decide which entry a build index selects.
func (d *Downloader) sortProductBuilds(doc map[string]any) (map[string]any, error) {
	order := d.cfg.GalaxyBuildSortingOrder
	if order == "" || order == "none" || len(doc) == 0 {
		return doc, nil
	}
	if order != "date" && order != "score" {
		return doc, nil
	}

	items, err := buildItems(doc)
	if err != nil {
		return nil, err
	}

	// Both orders start the same way: newest first. The branch is read here
	// too, because the score pass needs it and reading it once keeps the two
	// passes consistent.
	builds := make([]buildEntryDoc, len(items))
	for i, raw := range items {
		item, err := jsonval.Object(raw)
		if err != nil {
			return nil, fmt.Errorf("galaxy: builds items[%d]: %w", i, err)
		}
		date, err := jsonval.Str(item["date_published"])
		if err != nil {
			return nil, fmt.Errorf("galaxy: builds items[%d].date_published: %w", i, err)
		}
		branch, err := jsonval.Str(item["branch"])
		if err != nil {
			return nil, fmt.Errorf("galaxy: builds items[%d].branch: %w", i, err)
		}
		builds[i] = buildEntryDoc{item: raw, date: date, branch: branch}
	}
	sort.SliceStable(builds, func(i, j int) bool { return builds[i].date > builds[j].date })

	if order == "date" {
		doc["items"] = documentsOf(builds)
		return doc, nil
	}

	// score: the sorted position, with branch builds pushed behind and the
	// "compatibility" branch further still. The scores are frozen before the
	// second pass, because they are positions in the first one.
	scores := make([]int, len(builds))
	positions := make([]int, len(builds))
	for i, b := range builds {
		scores[i] = i
		if b.branch != "" {
			scores[i] += len(builds)
			if b.branch == "compatibility" {
				scores[i] += len(builds)
			}
		}
		positions[i] = i
	}
	sort.SliceStable(positions, func(i, j int) bool {
		return scores[positions[i]] < scores[positions[j]]
	})
	byScore := make([]any, len(positions))
	for i, pos := range positions {
		byScore[i] = builds[pos].item
	}
	doc["items"] = byScore
	return doc, nil
}

// buildEntryDoc is one build entry while the list is being reordered: the
// document itself plus the two fields the ordering reads.
type buildEntryDoc struct {
	item   any
	date   string
	branch string
}

// documentsOf collects the documents of a reordered build slice.
func documentsOf(builds []buildEntryDoc) []any {
	items := make([]any, len(builds))
	for i := range builds {
		items[i] = builds[i].item
	}
	return items
}

// buildItems returns the build entries of a builds document. A missing or null
// "items" is no builds at all; any other non-array value is a protocol error.
// The tier is the one the depot step established: absent means empty, the wrong
// shape means error.
func buildItems(doc map[string]any) ([]any, error) {
	raw, ok := doc["items"]
	if !ok || raw == nil {
		return nil, nil
	}
	items, err := jsonval.Array(raw)
	if err != nil {
		return nil, fmt.Errorf("galaxy: builds items: %w", err)
	}
	return items, nil
}

// buildIndexFor resolves the build argument: an exact build-id match wins,
// otherwise the argument is used as an index. The whole argument must be an
// integer; anything else means no build was selected (-1), which is what makes
// the command print the listing.
func buildIndexFor(items []any, buildID string) (int, error) {
	for i, raw := range items {
		item, err := jsonval.Object(raw)
		if err != nil {
			return 0, fmt.Errorf("galaxy: builds items[%d]: %w", i, err)
		}
		id, err := jsonval.Str(item["build_id"])
		if err != nil {
			return 0, fmt.Errorf("galaxy: builds items[%d].build_id: %w", i, err)
		}
		if id == buildID {
			return i, nil
		}
	}
	n, err := strconv.Atoi(buildID)
	if err != nil {
		return -1, nil
	}
	return n, nil
}

// buildRow reads one listing entry.
func buildRow(index int, item map[string]any) (BuildRow, error) {
	var row BuildRow
	var err error
	if row.VersionName, err = jsonval.Str(item["version_name"]); err != nil {
		return BuildRow{}, fmt.Errorf("galaxy: builds items[%d].version_name: %w", index, err)
	}
	if row.DatePublished, err = jsonval.Str(item["date_published"]); err != nil {
		return BuildRow{}, fmt.Errorf("galaxy: builds items[%d].date_published: %w", index, err)
	}
	if row.BuildID, err = jsonval.Str(item["build_id"]); err != nil {
		return BuildRow{}, fmt.Errorf("galaxy: builds items[%d].build_id: %w", index, err)
	}
	generation, err := jsonval.Int(item["generation"])
	if err != nil {
		return BuildRow{}, fmt.Errorf("galaxy: builds items[%d].generation: %w", index, err)
	}
	row.Generation = int(generation)
	row.Index = index
	return row, nil
}

// buildEntry reads the generation and link of one build entry. An index outside
// the list has no entry, so it reads as generation 0 with no link — which the
// switch in showBuildsFor turns into the "only generation 1 and 2" message.
func buildEntry(items []any, index int) (generation int, link string, err error) {
	if index < 0 || index >= len(items) {
		return 0, "", nil
	}
	item, err := jsonval.Object(items[index])
	if err != nil {
		return 0, "", fmt.Errorf("galaxy: builds items[%d]: %w", index, err)
	}
	gen, err := jsonval.Int(item["generation"])
	if err != nil {
		return 0, "", fmt.Errorf("galaxy: builds items[%d].generation: %w", index, err)
	}
	if link, err = jsonval.Str(item["link"]); err != nil {
		return 0, "", fmt.Errorf("galaxy: builds items[%d].link: %w", index, err)
	}
	return int(gen), link, nil
}

// endpointNames collects the non-empty endpoint names of a secure link
// document.
func endpointNames(doc map[string]any) ([]string, error) {
	raw, ok := doc["urls"]
	if !ok || raw == nil {
		return nil, nil
	}
	urls, err := jsonval.Array(raw)
	if err != nil {
		return nil, fmt.Errorf("galaxy: secure link urls: %w", err)
	}
	var names []string
	for i, rawEntry := range urls {
		entry, err := jsonval.Object(rawEntry)
		if err != nil {
			return nil, fmt.Errorf("galaxy: secure link urls[%d]: %w", i, err)
		}
		name, err := jsonval.Str(entry["endpoint_name"])
		if err != nil {
			return nil, fmt.Errorf("galaxy: secure link urls[%d].endpoint_name: %w", i, err)
		}
		if name != "" {
			names = append(names, name)
		}
	}
	return names, nil
}

// hashFromLink takes the last path segment of a build link, which is the hash
// ManifestV2 expects. A link without a "/" is the whole string.
func hashFromLink(link string) string {
	if i := strings.LastIndexByte(link, '/'); i >= 0 {
		return link[i+1:]
	}
	return link
}

// platformName maps the Galaxy platform mask onto the API path segment.
func platformName(platform uint32) string {
	switch platform {
	case config.PlatformLinux:
		return platformLinux
	case config.PlatformMac:
		return platformOsx
	default:
		return platformWindows
	}
}

const (
	platformWindows = "windows"
	platformOsx     = "osx"
	platformLinux   = "linux"
)
