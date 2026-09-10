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
)

// Messages the C++ source prints for these two commands instead of doing the
// work (downloader.cpp:4363,4391,4859-4861,4885,4908,4912). They are messages,
// not failures: main.cpp:842,897 never fold these commands into the exit code.
const (
	msgNoProducts        = "Didn't match any products"
	msgNoSelection       = "Unable to read selection"
	msgNoLinuxSupport    = "Galaxy API doesn't have Linux support"
	msgCheckInstallers   = "Checking for installers that can be used as repository"
	msgGenerationsOneTwo = "Only generation 1 and 2 builds are supported currently"
	msgGenerationTwoOnly = "Only generation 2 builds are supported currently"
)

// errInstallerFallback is returned after the two Linux messages. The C++ source
// continues into the game-details layer to list installers that can be used as
// a repository (downloader.cpp:4863-4898); that layer is not ported, and
// printing the two lines and exiting as though the fallback had run would report
// work that never happened.
var errInstallerFallback = errors.New(
	"the installer fallback for a platform without Galaxy builds is not implemented in this build")

// numericIDRE matches the product-id argument in its numeric form
// (downloader.cpp:3850).
var numericIDRE = regexp.MustCompile(`^[0-9]+$`)

// Notice is a message the C++ source prints instead of doing the work. It is
// not an error: the exit code stays 0. Err selects the stream, because the C++
// source splits them — the support and generation messages go to stdout, the
// argument-resolution failures to stderr.
type Notice struct {
	Text string
	Err  bool
}

// BuildRow is one line of the build listing printed when no build is selected
// (downloader.cpp:4906-4913).
//
// Fields are ordered to minimise padding: the strings first, then the ints.
type BuildRow struct {
	VersionName   string
	DatePublished string
	BuildID       string

	Generation int
	Index      int
}

// BuildsResult is what ShowBuilds produces. At most one of the three fields is
// set: Builds carries the listing when no build was selected, Manifest the
// document of the selected build, and Notice the message the C++ source prints
// when it stops early.
//
// Notice and an error can come back together: the Linux fallback prints its two
// lines and then reports that this build cannot continue, so the front end
// renders the result first and the error second.
type BuildsResult struct {
	Builds   []BuildRow
	Manifest map[string]any
	Notice   Notice
}

// CDNsResult is what ListCDNs produces: the endpoint names and, when the C++
// source stops early, the message it prints instead.
type CDNsResult struct {
	Names  []string
	Notice Notice
}

// ShowBuilds mirrors Downloader::galaxyShowBuilds (downloader.cpp:4830-4838 and
// 4840-4936): resolve the product id, read its build list, sort it the way
// --galaxy-builds-sort asks, then either return the listing or fetch and return
// the manifest of the selected build.
func (d *Downloader) ShowBuilds(ctx context.Context, productID, buildID string) (BuildsResult, error) {
	id, notice, err := d.selectProductID(ctx, productID)
	if err != nil {
		return BuildsResult{}, err
	}
	if notice.Text != "" {
		return BuildsResult{Notice: notice}, nil
	}
	if id == "" {
		// The C++ source skips the work when the resolved id is empty
		// (downloader.cpp:4833-4837).
		return BuildsResult{}, nil
	}
	return d.showBuildsFor(ctx, id, buildID)
}

// ListCDNs mirrors Downloader::galaxyListCDNs (downloader.cpp:4345-4353 and
// 4355-4397): resolve the product id, read the build list, and print the CDN
// endpoint names of the secure link of the selected build.
//
// Difference (recorded): the C++ source also extracts the build hash here and
// then never uses it. The extraction is dropped rather than carried as a dead
// variable.
func (d *Downloader) ListCDNs(ctx context.Context, productID, buildID string) (CDNsResult, error) {
	id, notice, err := d.selectProductID(ctx, productID)
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
	if index < 0 { // build_index = std::max(0, build_index) (downloader.cpp:4368)
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

// showBuildsFor is the by-id half of the C++ command
// (downloader.cpp:4840-4936).
func (d *Downloader) showBuildsFor(ctx context.Context, productID, buildID string) (BuildsResult, error) {
	platform := platformName(d.cfg.DownloadConfig.GalaxyPlatform)
	doc, err := d.galaxy.ProductBuilds(ctx, productID, platform, "")
	if err != nil {
		return BuildsResult{}, err
	}
	if doc, err = d.sortProductBuilds(doc); err != nil {
		return BuildsResult{}, err
	}

	// An empty answer for Linux is the C++ source's "the API has no Linux
	// support" case (downloader.cpp:4858-4863). An HTTP failure does not land
	// here: it is a *httpx.StatusError, unlike the C++ handle whose FAILONERROR
	// leaves the body empty (S13 Δ1).
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

// selectProductID mirrors Downloader::galaxySelectProductIdHelper
// (downloader.cpp:3845-3898): a numeric argument IS the product id; anything
// else is a game-name regular expression matched against the account's product
// list, with an interactive selection when it matches more than one.
//
// A non-empty Notice means the C++ source prints that message and stops; the
// returned error is reserved for a real failure of the product list itself.
//
// Difference (recorded): the C++ source overwrites the global sGameRegex with
// the argument before listing (downloader.cpp:3853). This port passes the
// pattern to the one listing call instead, so nothing shared is edited.
func (d *Downloader) selectProductID(ctx context.Context, productID string) (string, Notice, error) {
	if numericIDRE.MatchString(productID) {
		return productID, Notice{}, nil
	}

	res, err := catalog.List(ctx, d.web, d.gameListOptions(productID))
	if err != nil {
		return "", Notice{}, err
	}
	switch len(res.Games) {
	case 0:
		return "", Notice{Text: msgNoProducts, Err: true}, nil
	case 1:
		return res.Games[0].ID, Notice{}, nil
	}

	names := make([]string, 0, len(res.Games))
	for _, g := range res.Games {
		names = append(names, g.Name)
	}
	index, err := d.ui.SelectProduct(names)
	if err != nil || index < 0 || index >= len(names) {
		// The unanswerable-prompt case (downloader.cpp:3872-3876) and a console
		// that hands back an unusable index are one outcome here: no selection
		// was made.
		return "", Notice{Text: msgNoSelection, Err: true}, nil
	}
	return res.Games[index].ID, Notice{}, nil
}

// gameListOptions assembles the product query for the game-name lookup. It
// mirrors what the C++ source reaches through getGameList() → getGames()
// (downloader.cpp:381-384, website.cpp:92-166).
//
// It duplicates the assembly in internal/cli/list.go on purpose: S17 leaves the
// listing command where it is (review ruling D17=b), and the two copies converge
// when that command moves into this package. Keeping them separate keeps this
// step from changing listing behaviour.
func (d *Downloader) gameListOptions(gameRegex string) catalog.ListOptions {
	cfg := d.cfg
	return catalog.ListOptions{
		Tags:              cfg.DownloadConfig.Tags,
		GameRegex:         gameRegex,
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

// sortProductBuilds mirrors Downloader::sortGalaxyProductBuilds
// (downloader.cpp:6824-6890): the requested order reorders the build list, and
// anything else — empty, "none" or an unknown value — leaves it alone, because
// the C++ source only writes the list back inside its two named branches.
//
// Difference (recorded): the C++ source uses std::sort, which does not define
// the order of entries with equal keys; this port uses sort.SliceStable and
// keeps the document order for them. The difference matters here — equal keys
// are what decide which entry a build index selects.
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

	// Both branches start the same way: newest first (downloader.cpp:6836-6843,
	// 6851-6858). The branch is read here too, because the score pass needs it
	// and reading it once keeps the two passes consistent.
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
	// "compatibility" branch further still (downloader.cpp:6860-6885). The
	// scores are frozen before the second pass, because they are positions in
	// the first one.
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

// buildIndexFor mirrors Downloader::galaxyGetBuildIndexWithBuildId
// (downloader.cpp:3811-3839): an exact build-id match wins, otherwise the
// argument is used as an index. -1 means no build was selected, which is what
// makes the command print the listing.
//
// Difference (recorded): the C++ source uses std::stoi, which accepts leading
// whitespace and stops at the first non-digit, so "2x" means build 2; this port
// requires the whole argument to be an integer.
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
		// The C++ source catches the conversion failure and keeps -1.
		return -1, nil
	}
	return n, nil
}

// buildRow reads one listing entry (downloader.cpp:4906-4913).
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
// the list has no entry, exactly like the C++ source reading a null value, so
// it reads as generation 0 with no link — which its switch turns into the
// "only generation 1 and 2" message.
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

// endpointNames collects the non-empty endpoint names of a secure link document
// (downloader.cpp:4383-4392).
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
// ManifestV2 expects (downloader.cpp:4927-4928). A link without a "/" is the
// whole string, mirroring the C++ arithmetic where a missing separator sends
// the begin iterator to offset 0.
func hashFromLink(link string) string {
	if i := strings.LastIndexByte(link, '/'); i >= 0 {
		return link[i+1:]
	}
	return link
}

// platformName maps the Galaxy platform mask onto the API path segment
// (downloader.cpp:4843-4849).
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
