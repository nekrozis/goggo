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
	"github.com/nekrozis/goggo/internal/reconcile"
	"github.com/nekrozis/goggo/internal/util"
)

// msgLevelVerbose is the message level that turns on the verbose gates of the
// plan builder. A local constant keeps this package free of a ui/log import for
// one value.
const msgLevelVerbose = 1

// PlanResult is one plan-building run's outcome: the plan plus every display
// message produced along the way. core does not print; the front end renders
// the messages.
//
// InstallPath is the installation root the plan was built against (D81): the
// small-files containers and the orphan check need the same directory the
// plan's destinations were derived from, and an empty or fully filtered plan
// cannot yield it any other way.
type PlanResult struct {
	Plan        model.DownloadPlan
	Messages    []Notice
	InstallPath string

	// Skipped is the planning-time observation report: destinations that
	// already satisfied their item when the plan was built, so no transfer
	// task was created for them. It is a report, not the correctness
	// boundary — Install revalidates the whole set after the transfer
	// (revalidateSkipped) and fails if anything changed meanwhile.
	Skipped []SkippedFile

	// Expected is the set of destinations the target installation is expected
	// to have once it is installed, in path order, and it is the ONLY file-set
	// a read-only consumer may work from: rebuilding the set from Tasks and
	// Skipped instead would let the two drift.
	//
	// It differs from the transfer tasks in both directions: a skipped
	// destination is in it without a task, and the small-files containers are
	// NOT in it — they are unpacked into their members and deleted while the
	// install runs (see ExtractSmallFilesContainers). Like the orphan check's
	// ledger it describes which paths the installation owns (D44).
	Expected []InstalledFile
}

// InstalledFile is one destination the finished installation is expected to
// have: the depot item and where it lives. Size and hash are available through
// Item.
type InstalledFile struct {
	Destination string
	Item        model.GalaxyDepotItem
}

// SkippedFile is one destination the plan observed as already up to date.
// Size is available through Item.TotalSize.
type SkippedFile struct {
	Destination string
	Item        model.GalaxyDepotItem
}

// addMessage appends a non-error display message when it carries text.
func (r *PlanResult) addMessage(text string) {
	if text != "" {
		r.Messages = append(r.Messages, Notice{Text: text})
	}
}

// planMode says what a plan is built for.
//
// Both modes define WHICH files the installation owns identically — product and
// build resolution, depot expansion, include mask, dependencies, blacklist, SFC
// decision and install root — and differ only in the install-shaped display and
// pre-flight, which a read-only verification should not pay for.
type planMode uint8

const (
	// planForInstall is the plan an install runs: it also carries the
	// previous build's diff, the summary block the install prints and the
	// free-space gate.
	planForInstall planMode = iota

	// planForReadOnly is the plan a read-only consumer observes: the same file
	// set and the same root, without the install-shaped display, without a
	// second manifest fetch for the old build and without a free-space answer
	// (nothing is downloaded). A verification and an orphan walk both read it,
	// which is why it is named for what it is rather than for one of them.
	planForReadOnly
)

// BuildPlan resolves one install request into a download plan, without
// downloading anything and without writing to the install directory: the
// filesystem is only read (the small-files container probes and the
// previously-installed build's info file).
//
// Install calls this and then runs the plan. A read-only consumer uses Verify
// or CheckOrphanedFiles instead, which build the plan in planForReadOnly mode.
func (d *Downloader) BuildPlan(ctx context.Context, req InstallRequest) (PlanResult, error) {
	return d.buildPlan(ctx, req, planForInstall)
}

// buildPlan is BuildPlan with the mode supplied; see planMode for what the two
// modes share and what planForInstall adds.
func (d *Downloader) buildPlan(ctx context.Context, req InstallRequest, mode planMode) (PlanResult, error) {
	var res PlanResult

	// Product resolution: a numeric id passes through, a name goes through the
	// account's game list, possibly interactively.
	id, notice, err := d.selectProductID(ctx, req.ProductID)
	res.addMessage(notice.Text)
	if err != nil {
		return res, err
	}

	// Builds and their order. The generation query parameter stays unset,
	// which the client fills with its default "2".
	builds, err := d.galaxy.ProductBuilds(ctx, id, req.Platform, "")
	if err != nil {
		return res, err
	}
	builds, err = d.sortProductBuilds(builds)
	if err != nil {
		return res, err
	}

	// An empty document on a Linux target is the installer fallback's cue.
	// That path is not implemented in this build, so report it and stop loudly
	// instead of pretending the installers path ran.
	items, err := buildsItems(builds)
	if err != nil {
		return res, err
	}
	if len(builds) == 0 && req.Platform == platformLinux {
		res.addMessage(msgNoLinuxSupport)
		res.addMessage(msgCheckInstallers)
		return res, fmt.Errorf("linux installer fallback: %w", ErrNotImplemented)
	}

	// Build index and generation gate. The index clamps at zero, and an absent
	// items entry reads as generation 0, which fails the gate with the message
	// rather than an error.
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

	// The build id is the tail of the link; a link without a slash is used
	// whole.
	link, err := buildLink(items, index)
	if err != nil {
		return res, err
	}
	buildHash := link[strings.LastIndexByte(link, '/')+1:]

	// The new manifest and the game title.
	manifest, err := d.galaxy.ManifestV2(ctx, buildHash, false)
	if err != nil {
		return res, err
	}
	gameTitle := manifestProductName(manifest)

	// Install directory and path. Templates whose value comes from the product
	// document trigger the fetch here, so the resolver itself stays a pure
	// function of the two documents. The credential refresh is the run's usual
	// one, and it happens only when a request is actually about to go out.
	installDirectory := ""
	if d.cfg.Directories.SubDirectories {
		var product map[string]any
		if InstallSubdirNeedsProductInfo(req.SubdirTemplate) {
			baseID, err := documentString(manifest, "baseProductId")
			if err != nil {
				return res, err
			}
			// An empty base product id means there is no product to ask
			// about, so no request is made and the template falls back to its
			// literal text.
			if baseID != "" {
				refresh := tokenRefresher{refresh: d.refreshAndSave, expired: func() bool { return d.token.IsExpired() }}
				if err := refresh.refreshIfExpired(ctx); err != nil {
					return res, fmt.Errorf("galaxy: refresh login: %w", err)
				}
				if product, err = d.galaxy.Product(ctx, baseID); err != nil {
					return res, err
				}
			}
		}
		installDirectory, err = ResolveInstallSubdir(req.SubdirTemplate, manifest, product)
		if err != nil {
			return res, err
		}
	}
	installPath := d.cfg.Directories.Directory + installDirectory
	res.InstallPath = installPath

	// The depot items.
	planItems, err := d.resolveDepotItems(ctx, manifest, req)
	if err != nil {
		return res, err
	}

	// The blacklist filter: a planned file whose install path matches drops out
	// of the plan.
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

	// The small-files container decision, which reads the install directory: if
	// any file that lives inside the container is already on disk, the
	// container is dropped and those files download normally; otherwise the
	// container downloads and the files wait for the post-transfer extraction.
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

	// Differences from the previously installed build. The comparison runs
	// after the SFC decision, so a file that moved into the container counts as
	// deleted (D55). Only an install uses this: the deletes are what it
	// performs, and finding them costs a second manifest fetch plus a second
	// depot expansion, which a verification has no use for.
	previousBuildDeletes := func() ([]string, error) {
		var deletes []string
		infoPath := installPath + "/goggame-" + id + ".info"
		if _, err := os.Stat(infoPath); err == nil {
			oldBuildID, err := readInfoBuildID(infoPath)
			if err != nil {
				return nil, err
			}
			if oldBuildID != "" {
				oldIndex, err := buildIndexFor(items, oldBuildID)
				if err != nil {
					return nil, err
				}
				// The current index must differ for there to be a diff at all; the
				// same build is never compared against itself.
				if oldIndex >= 0 && oldIndex != index {
					oldLink, err := buildLink(items, oldIndex)
					if err != nil {
						return nil, err
					}
					oldHash := oldLink[strings.LastIndexByte(oldLink, '/')+1:]
					oldManifest, err := d.galaxy.ManifestV2(ctx, oldHash, false)
					if err != nil {
						return nil, err
					}
					oldItems, err := d.resolveDepotItems(ctx, oldManifest, req)
					if err != nil {
						return nil, err
					}
					current := map[string]bool{}
					for _, it := range planItems {
						current[it.Path] = true
					}
					for _, old := range oldItems {
						if current[old.Path] {
							continue
						}
						// The message is emitted before existence is tested,
						// so a path that is not on disk still gets its line
						// (D57).
						filepath := installPath + "/" + old.Path
						deletes = append(deletes, filepath)
						res.addMessage("Deleting " + filepath)
					}
				}
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		return deletes, nil
	}
	var deletes []string
	if mode == planForInstall {
		if deletes, err = previousBuildDeletes(); err != nil {
			return res, err
		}
	}

	// The expected file set: what the finished installation must have, whatever
	// route the bytes take. A small-files container is transport-only — it is
	// unpacked and removed — so it never belongs to the set, while its members
	// do. Fixed order, by path: the order a consumer reports in is part of the
	// plan, not a renderer's choice.
	expected := make([]model.GalaxyDepotItem, 0, len(planItems)+len(sfcItems))
	expected = append(expected, planItems...)
	expected = append(expected, sfcItems...)
	for _, it := range expected {
		if it.IsSmallFilesContainer {
			continue
		}
		res.Expected = append(res.Expected, InstalledFile{
			Destination: installPath + "/" + it.Path,
			Item:        it,
		})
	}
	sort.Slice(res.Expected, func(i, j int) bool {
		return res.Expected[i].Item.Path < res.Expected[j].Item.Path
	})

	// The queue summary: the verbose listing, then
	// the title, the file count and the installed size. totalSize sums the
	// FINAL transfer tasks — after the blacklist, SFC and skip filtering —
	// because that is what the free-space gate answers for: the bytes that
	// will actually be fetched.
	tasks := make([]model.FileTask, 0, len(planItems))
	totalSize := uint64(0)
	skipped := 0
	for _, it := range planItems {
		if d.cfg.MsgLevel >= msgLevelVerbose {
			res.addMessage(it.Path)
			res.addMessage(fmt.Sprintf("\tChunks: %d", len(it.Chunks)))
			res.addMessage(fmt.Sprintf("\tmd5: %s", it.MD5))
		}
		destination := installPath + "/" + it.Path

		// The plan-level reconciliation: a destination that already satisfies
		// the item — same uncompressed size and whole-file md5 — leaves the
		// queue here, so the transfer only sees real work. The transfer
		// re-checks the destination authoritatively at task start, and the
		// install is gated by revalidating the skipped set at the end. An
		// observation failure fails the plan; it is never turned into a
		// destructive action (D43).
		complete, err := reconcile.IsComplete(it, destination)
		if err != nil {
			return res, fmt.Errorf("Failed to inspect %s: %w", destination, err)
		}
		if complete {
			skipped++
			// The per-file ": OK" is the reconcile record, not user output: N
			// repeats of one path are one aggregate state. Verbose keeps the
			// per-object lines.
			if d.cfg.MsgLevel >= msgLevelVerbose {
				res.addMessage(destination + ": OK")
			}
			res.Skipped = append(res.Skipped, SkippedFile{Destination: destination, Item: it})
			continue
		}
		// Only real transfer work counts toward the download total: a skipped
		// file fetches no bytes, so it must not inflate the free-space gate.
		totalSize += it.TotalSize
		tasks = append(tasks, model.FileTask{Item: it, Destination: destination})
	}
	// The install-shaped summary block belongs to the install's display: it
	// says what it is about to do and what it will fetch. A read-only consumer
	// shows its own summary instead of printing "Installing →".
	if mode == planForInstall {
		res.addMessage(gameTitle)
		// The header carries the shared context once: the task rows are
		// installPath-relative, so the root belongs here, not on every line.
		res.addMessage("Installing → " + installPath)
		res.addMessage(fmt.Sprintf("Files: %d", len(tasks)))
		if skipped > 0 {
			res.addMessage(fmt.Sprintf("Already up to date: %d files", skipped))
			if len(tasks) == 0 {
				// The zero-transfer fast path as a final state: nothing enters
				// the live UI because transfer.Run has no tasks, and the lines
				// say exactly that.
				res.addMessage("Nothing to download.")
			}
		}
		res.addMessage("Total size installed: " + util.SizeString(totalSize, d.cfg.UnitFormat))
	}

	// The free-space gate, when the option is on: the space of the nearest
	// existing ancestor of the install path — the install directory itself may
	// not exist yet — is compared against the uncompressed total. Failure is an
	// error for the front end to report, not a process exit. A verification
	// transfers nothing, so the answer is not its business.
	if mode == planForInstall && d.cfg.DownloadConfig.FreeSpaceCheck {
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

// nearestExistingDir walks up from path to the closest directory that exists.
// The volume query then runs against that directory, because the install
// directory itself may not exist yet. ok is false when nothing up to the root
// exists, and the caller skips the check.
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

// sfcGroupsFor pairs each container with the files whose product id matches.
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

// buildLink reads one build entry's link.
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

// manifestProductName reads products[0].name; a document without products names
// nothing.
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
// empty slice, a present non-array is an error — the split D9 fixed for
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

// resolveDepotItems expands every depot, drops DLC entries when the include
// mask does not ask for them, adds the selected dependencies, stamps product
// ids, renames small-files containers and deduplicates by path.
func (d *Downloader) resolveDepotItems(ctx context.Context, manifest map[string]any, req InstallRequest) ([]model.GalaxyDepotItem, error) {
	baseProductID, err := documentString(manifest, "baseProductId")
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

	// DLC filter.
	if d.cfg.DownloadConfig.Include&config.GFDLC == 0 {
		var baseOnly []model.GalaxyDepotItem
		for _, it := range items {
			if it.ProductID == baseProductID {
				baseOnly = append(baseOnly, it)
			}
		}
		items = baseOnly
	}

	// Dependencies: the manifest names the dependency ids it wants, the
	// repository maps them to depots, and a selected dependency depot expands
	// with is_dependency set.
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
			// An empty document, or one without "depots", adds nothing.
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

	// Stamp product ids and rename small-files containers.
	for i := range items {
		if items[i].ProductID == "" {
			items[i].ProductID = baseProductID
		}
		if items[i].IsSmallFilesContainer {
			items[i].Path += "_" + items[i].ProductID
		}
	}

	// Deduplicate by path: a same-path duplicate with the same md5 is dropped,
	// and one with a different md5 replaces the base game's entry when it comes
	// from a DLC. The output is ordered by path.
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
// matches osBitness against; an unmatched flag falls back to 64-bit.
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

// readInfoBuildID reads buildId out of a goggame-<product>.info file. A parse
// failure continues with an empty document, which means no diff; a structured
// buildId is an error.
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
