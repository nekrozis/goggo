package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nekrozis/goggo/internal/blacklist"
	"github.com/nekrozis/goggo/internal/config"
	"github.com/nekrozis/goggo/internal/galaxy"
	"github.com/nekrozis/goggo/internal/jsonval"
	"github.com/nekrozis/goggo/internal/model"
	"github.com/nekrozis/goggo/internal/util"
)

// msgLevelVerbose mirrors MSGLEVEL_VERBOSE (message.h:19) for the verbose
// gates of the plan builder. A local constant keeps this package free of a
// ui/log import for one value.
const msgLevelVerbose = 1

// PlanResult is one plan-building run's outcome: the plan plus every display
// message the C++ source printed along the way. core does not print; the front
// end renders the messages.
//
// InstallPath is the installation root the plan was built against (review
// D81): the small-files containers and the orphan check need the same
// directory the plan's destinations were derived from, and an empty or fully
// filtered plan cannot yield it any other way.
type PlanResult struct {
	Plan        model.DownloadPlan
	Messages    []Notice
	InstallPath string
}

// addMessage appends a non-error display message when it carries text.
func (r *PlanResult) addMessage(text string) {
	if text != "" {
		r.Messages = append(r.Messages, Notice{Text: text})
	}
}

// BuildPlan resolves one install request into a download plan, without
// downloading anything and without writing to the install directory: the
// filesystem is only read (the small-files container probes and the
// previously-installed build's info file).
//
// It mirrors the plan-construction part of Downloader::galaxyInstallGameById
// (downloader.cpp:4032-4253). The engine that would consume the plan is not
// wired to Install yet, so nothing can reach the transfer layer from here.
func (d *Downloader) BuildPlan(ctx context.Context, req InstallRequest) (PlanResult, error) {
	var res PlanResult

	// Product resolution (main.cpp:886 -> galaxyInstallGame ->
	// galaxySelectProductIdHelper): a numeric id passes through, a name goes
	// through the account's game list, possibly interactively.
	id, notice, err := d.selectProductID(ctx, req.ProductID)
	res.addMessage(notice.Text)
	if err != nil {
		return res, err
	}

	// Builds and their order (downloader.cpp:4040-4041). The generation query
	// parameter stays unset, which the client fills with its default "2".
	builds, err := d.galaxy.ProductBuilds(ctx, id, req.Platform, "")
	if err != nil {
		return res, err
	}
	builds, err = d.sortProductBuilds(builds)
	if err != nil {
		return res, err
	}

	// An empty document on a Linux target is the MojoSetup fallback's cue
	// (downloader.cpp:4048-4057). That path needs product information this
	// build does not port, so report it like the C++ source does and stop
	// loudly instead of pretending the installers path ran.
	items, err := buildsItems(builds)
	if err != nil {
		return res, err
	}
	if len(builds) == 0 && req.Platform == platformLinux {
		res.addMessage(msgNoLinuxSupport)
		res.addMessage(msgCheckInstallers)
		return res, fmt.Errorf("linux installer fallback: %w", ErrNotImplemented)
	}

	// Build index and generation gate (downloader.cpp:4058-4067). The index
	// clamps at zero, and an absent items entry reads as generation 0 — the
	// same null the C++ subscript produces — which fails the gate with the
	// message rather than an error.
	index, err := buildIndexFor(items, req.BuildID)
	if err != nil {
		return res, err
	}
	if index < 0 {
		index = 0
	}
	generation := 0
	if index < len(items) {
		entry, err := jsonval.Object(items[index])
		if err != nil {
			return res, fmt.Errorf("galaxy: builds items[%d]: %w", index, err)
		}
		gen, err := jsonval.Int(entry["generation"])
		if err != nil {
			return res, fmt.Errorf("galaxy: builds items[%d].generation: %w", index, err)
		}
		generation = int(gen)
	}
	if generation != 2 {
		res.addMessage(msgGenerationsOneTwo)
		return res, nil
	}

	// The build id is the tail of the link (downloader.cpp:4069-4071). Upstream
	// indexes from find_last_of("/")+1, which wraps to the string start when
	// there is no slash — the slice below does the same.
	link, err := buildLink(items, index)
	if err != nil {
		return res, err
	}
	buildHash := link[strings.LastIndexByte(link, '/')+1:]

	// The new manifest and the game title (downloader.cpp:4073-4075).
	manifest, err := d.galaxy.ManifestV2(ctx, buildHash, false)
	if err != nil {
		return res, err
	}
	gameTitle := manifestProductName(manifest)

	// Install directory and path (downloader.cpp:4076-4081).
	installDirectory := ""
	if d.cfg.Directories.SubDirectories {
		installDirectory, err = ResolveInstallSubdir(req.SubdirTemplate, manifest)
		if err != nil {
			return res, err
		}
	}
	installPath := d.cfg.Directories.Directory + installDirectory
	res.InstallPath = installPath

	// The depot items (galaxyGetDepotItemVectorFromJson, 3900-4020).
	planItems, err := d.resolveDepotItems(ctx, manifest, req)
	if err != nil {
		return res, err
	}

	// The blacklist filter (downloader.cpp:4084-4100): a planned file whose
	// install path matches drops out of the plan.
	bl, err := blacklist.LoadBlacklist(d.cfg.BlacklistFilePath)
	if err != nil {
		return res, err
	}
	for _, line := range bl.Diagnostics() {
		res.addMessage(line)
	}
	var filtered []model.GalaxyDepotItem
	for _, it := range planItems {
		p := installPath + "/" + it.Path
		if bl.IsBlacklisted(p) {
			if d.cfg.MsgLevel >= msgLevelVerbose {
				res.addMessage("Skipping blacklisted file: " + p)
			}
			continue
		}
		filtered = append(filtered, it)
	}
	planItems = filtered

	// The small-files container decision (downloader.cpp:4102-4131), which
	// reads the install directory: if any file that lives inside the container
	// is already on disk, the container is dropped and those files download
	// normally; otherwise the container downloads and the files wait for the
	// post-transfer extraction (S20).
	useSFC := true
	for _, it := range planItems {
		if it.IsInSFC {
			if _, err := os.Stat(installPath + "/" + it.Path); err == nil {
				useSFC = false
				break
			}
		}
	}
	var sfcContainers []model.GalaxyDepotItem
	var sfcItems []model.GalaxyDepotItem
	if !useSFC {
		var kept []model.GalaxyDepotItem
		for _, it := range planItems {
			if !it.IsSmallFilesContainer {
				kept = append(kept, it)
			}
		}
		planItems = kept
	} else {
		var kept []model.GalaxyDepotItem
		for _, it := range planItems {
			if it.IsSmallFilesContainer {
				sfcContainers = append(sfcContainers, it)
			}
			if it.IsInSFC {
				sfcItems = append(sfcItems, it)
				continue
			}
			kept = append(kept, it)
		}
		planItems = kept
	}

	// Differences from the previously installed build
	// (downloader.cpp:4134-4196). The comparison runs after the SFC decision,
	// so a file that moved into the container counts as deleted — upstream as
	// written (review D55).
	var deletes []string
	infoPath := installPath + "/goggame-" + id + ".info"
	if _, err := os.Stat(infoPath); err == nil {
		oldBuildID, err := readInfoBuildID(infoPath)
		if err != nil {
			return res, err
		}
		if oldBuildID != "" {
			oldIndex, err := buildIndexFor(items, oldBuildID)
			if err != nil {
				return res, err
			}
			// The current index must differ for there to be a diff at all; the
			// same build is never compared against itself.
			if oldIndex >= 0 && oldIndex != index {
				oldLink, err := buildLink(items, oldIndex)
				if err != nil {
					return res, err
				}
				oldHash := oldLink[strings.LastIndexByte(oldLink, '/')+1:]
				oldManifest, err := d.galaxy.ManifestV2(ctx, oldHash, false)
				if err != nil {
					return res, err
				}
				oldItems, err := d.resolveDepotItems(ctx, oldManifest, req)
				if err != nil {
					return res, err
				}
				current := map[string]bool{}
				for _, it := range planItems {
					current[it.Path] = true
				}
				for _, old := range oldItems {
					if current[old.Path] {
						continue
					}
					// The C++ source prints before it tests for existence, so
					// a path that is not on disk still gets its line (D57).
					filepath := installPath + "/" + old.Path
					deletes = append(deletes, filepath)
					res.addMessage("Deleting " + filepath)
				}
			}
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return res, err
	}

	// The queue summary (downloader.cpp:4198-4212): the verbose listing, then
	// the title, the file count and the installed size. totalSize sums the
	// FINAL tasks — after the blacklist and SFC filtering — because that is
	// what the free-space gate answers for.
	tasks := make([]model.FileTask, 0, len(planItems))
	totalSize := uint64(0)
	for _, it := range planItems {
		if d.cfg.MsgLevel >= msgLevelVerbose {
			res.addMessage(it.Path)
			res.addMessage(fmt.Sprintf("\tChunks: %d", len(it.Chunks)))
			res.addMessage(fmt.Sprintf("\tmd5: %s", it.MD5))
		}
		totalSize += it.TotalSize
		tasks = append(tasks, model.FileTask{Item: it, Destination: installPath + "/" + it.Path})
	}
	res.addMessage(gameTitle)
	res.addMessage(fmt.Sprintf("Files: %d", len(tasks)))
	res.addMessage("Total size installed: " + util.SizeString(totalSize, d.cfg.UnitFormat))

	// The free-space gate (downloader.cpp:4214-4233), when the option is on:
	// the space of the nearest existing ancestor of the install path — the
	// install directory itself may not exist yet — is compared against the
	// uncompressed total. Failure is an error for the front end to report, not
	// a process exit.
	if d.cfg.DownloadConfig.FreeSpaceCheck {
		if volume, ok := nearestExistingDir(installPath); ok {
			available, err := freeSpaceAvailable(volume)
			if err != nil {
				return res, err
			}
			if available < totalSize {
				return res, fmt.Errorf("not enough free space in %s (%s)",
					filepath.Clean(volume), util.SizeString(available, d.cfg.UnitFormat))
			}
		}
	}

	res.Plan = model.DownloadPlan{
		Tasks:   tasks,
		Deletes: deletes,
		SFC:     sfcGroupsFor(sfcContainers, sfcItems),
	}
	return res, nil
}

// nearestExistingDir walks up from path to the closest directory that exists,
// as boost::filesystem::absolute and the parent loop in
// downloader.cpp:4217-4222 do. The volume query then runs against that
// directory, because the install directory itself may not exist yet. ok is
// false when nothing up to the root exists, and the caller skips the check —
// also upstream's behaviour.
func nearestExistingDir(path string) (string, bool) {
	dir := path
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	for dir != "" {
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", false
}

// sfcGroupsFor pairs each container with the files whose product id matches,
// mirroring the extraction loop in downloader.cpp:4270-4285.
func sfcGroupsFor(containers, items []model.GalaxyDepotItem) []model.SFCGroup {
	var groups []model.SFCGroup
	for _, container := range containers {
		group := model.SFCGroup{Container: container}
		for _, it := range items {
			if it.ProductID == container.ProductID {
				group.Items = append(group.Items, it)
			}
		}
		groups = append(groups, group)
	}
	return groups
}

// buildsItems reads a builds document's items array: a missing or null member
// is no items, a present non-array is an error.
func buildsItems(builds map[string]any) ([]any, error) {
	raw, ok := builds["items"]
	if !ok || raw == nil {
		return nil, nil
	}
	items, err := jsonval.Array(raw)
	if err != nil {
		return nil, fmt.Errorf("galaxy: builds items: %w", err)
	}
	return items, nil
}

// buildLink reads one build entry's link (downloader.cpp:4070).
func buildLink(items []any, index int) (string, error) {
	if index < 0 || index >= len(items) {
		return "", fmt.Errorf("galaxy: builds items[%d]: out of range", index)
	}
	entry, err := jsonval.Object(items[index])
	if err != nil {
		return "", fmt.Errorf("galaxy: builds items[%d]: %w", index, err)
	}
	link, err := jsonval.Str(entry["link"])
	if err != nil {
		return "", fmt.Errorf("galaxy: builds items[%d].link: %w", index, err)
	}
	return link, nil
}

// manifestProductName reads products[0].name (downloader.cpp:4075); a document
// without products names nothing.
func manifestProductName(manifest map[string]any) string {
	raw, ok := manifest["products"]
	if !ok || raw == nil {
		return ""
	}
	products, err := jsonval.Array(raw)
	if err != nil || len(products) == 0 {
		return ""
	}
	entry, err := jsonval.Object(products[0])
	if err != nil {
		return ""
	}
	name, err := jsonval.Str(entry["name"])
	if err != nil {
		return ""
	}
	return name
}

// manifestArray reads an optional array member: a missing or null member is an
// empty slice, a present non-array is an error — the split review D9 fixed for
// container members.
func manifestArray(manifest map[string]any, key string) ([]any, error) {
	raw, ok := manifest[key]
	if !ok || raw == nil {
		return nil, nil
	}
	v, err := jsonval.Array(raw)
	if err != nil {
		return nil, fmt.Errorf("galaxy: manifest %s: %w", key, err)
	}
	return v, nil
}

// resolveDepotItems is galaxyGetDepotItemVectorFromJson
// (downloader.cpp:3900-4020): expand every depot, drop DLC entries when the
// include mask does not ask for them, add the selected dependencies, stamp
// product ids, rename small-files containers and deduplicate by path.
func (d *Downloader) resolveDepotItems(ctx context.Context, manifest map[string]any, req InstallRequest) ([]model.GalaxyDepotItem, error) {
	baseProductID, err := manifestString(manifest, "baseProductId")
	if err != nil {
		return nil, err
	}

	depots, err := manifestArray(manifest, "depots")
	if err != nil {
		return nil, err
	}
	depotOpts := galaxy.DepotOptions{
		LowercasePaths: d.cfg.DownloadConfig.GalaxyLowercasePath,
		Platform:       d.cfg.DownloadConfig.GalaxyPlatform,
	}
	var items []model.GalaxyDepotItem
	for i, raw := range depots {
		depot, err := jsonval.Object(raw)
		if err != nil {
			return nil, fmt.Errorf("galaxy: manifest depots[%d]: %w", i, err)
		}
		vec, err := d.galaxy.FilteredDepotItems(ctx, depot, req.LanguageRegex, archCode(req.Arch), depotOpts)
		if err != nil {
			return nil, err
		}
		items = append(items, vec...)
	}

	// DLC filter (downloader.cpp:3935-3943).
	if d.cfg.DownloadConfig.Include&config.GFDLC == 0 {
		var baseOnly []model.GalaxyDepotItem
		for _, it := range items {
			if it.ProductID == baseProductID {
				baseOnly = append(baseOnly, it)
			}
		}
		items = baseOnly
	}

	// Dependencies (downloader.cpp:3946-3972): the manifest names the
	// dependency ids it wants, the repository maps them to depots, and a
	// selected dependency depot expands with is_dependency set.
	if d.cfg.DownloadConfig.GalaxyDependencies {
		ids, err := manifestArray(manifest, "dependencies")
		if err != nil {
			return nil, err
		}
		var wanted []string
		for i, raw := range ids {
			id, err := jsonval.Str(raw)
			if err != nil {
				return nil, fmt.Errorf("galaxy: manifest dependencies[%d]: %w", i, err)
			}
			wanted = append(wanted, id)
		}
		if len(wanted) > 0 {
			depDoc, err := d.galaxy.DependenciesJSON(ctx)
			if err != nil {
				return nil, err
			}
			// Upstream: an empty document, or one without "depots", adds
			// nothing (downloader.cpp:3956).
			raw, ok := depDoc["depots"]
			if ok && raw != nil {
				depotDocs, err := jsonval.Array(raw)
				if err != nil {
					return nil, fmt.Errorf("galaxy: dependency repository depots: %w", err)
				}
				for i, raw := range depotDocs {
					depot, err := jsonval.Object(raw)
					if err != nil {
						return nil, fmt.Errorf("galaxy: dependency repository depots[%d]: %w", i, err)
					}
					depID, err := jsonval.Str(depot["dependencyId"])
					if err != nil {
						return nil, fmt.Errorf("galaxy: dependency repository depots[%d].dependencyId: %w", i, err)
					}
					if !containsString(wanted, depID) {
						continue
					}
					vec, err := d.galaxy.FilteredDepotItems(ctx, depot, req.LanguageRegex, archCode(req.Arch), galaxy.DepotOptions{
						IsDependency:   true,
						LowercasePaths: d.cfg.DownloadConfig.GalaxyLowercasePath,
						Platform:       d.cfg.DownloadConfig.GalaxyPlatform,
					})
					if err != nil {
						return nil, err
					}
					items = append(items, vec...)
				}
			}
		}
	}

	// Stamp product ids and rename small-files containers
	// (downloader.cpp:3975-3986).
	for i := range items {
		if items[i].ProductID == "" {
			items[i].ProductID = baseProductID
		}
		if items[i].IsSmallFilesContainer {
			items[i].Path += "_" + items[i].ProductID
		}
	}

	// Deduplicate by path (downloader.cpp:3989-4011): a same-path duplicate
	// with the same md5 is dropped, and one with a different md5 replaces the
	// base game's entry when it comes from a DLC. Upstream collects into a
	// std::map, whose iteration orders the output by path.
	byPath := map[string]model.GalaxyDepotItem{}
	for _, it := range items {
		prev, ok := byPath[it.Path]
		if !ok {
			byPath[it.Path] = it
			continue
		}
		if prev.MD5 == it.MD5 {
			continue
		}
		if it.ProductID != baseProductID {
			byPath[it.Path] = it
		}
	}
	paths := make([]string, 0, len(byPath))
	for path := range byPath {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	deduped := make([]model.GalaxyDepotItem, 0, len(paths))
	for _, path := range paths {
		deduped = append(deduped, byPath[path])
	}
	return deduped, nil
}

// archCode maps a Galaxy architecture flag onto the code the depot filter
// matches osBitness against (downloader.cpp:3916-3922); an unmatched flag
// falls back to 64-bit, as the C++ loop does when it finds nothing.
func archCode(flag uint32) string {
	if o, ok := util.OptionByID(flag, config.GalaxyArchs); ok {
		return o.Code
	}
	return "64"
}

// containsString reports whether needle is one of the entries.
func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// readInfoBuildID reads buildId out of a goggame-<product>.info file
// (downloader.cpp:4143-4146). Upstream's reader reports a parse failure and
// continues with an empty document, which means no diff; this port does the
// same. A structured buildId is an error rather than the crash jsoncpp's
// asString would raise.
func readInfoBuildID(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return "", nil
	}
	if doc == nil {
		return "", nil
	}
	return jsonval.Str(doc["buildId"])
}
