package core

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/nekrozis/goggo/internal/blacklist"
	"github.com/nekrozis/goggo/internal/config"
	"github.com/nekrozis/goggo/internal/gamedetails"
	"github.com/nekrozis/goggo/internal/model"
	"github.com/nekrozis/goggo/internal/transfer"
	"github.com/nekrozis/goggo/internal/util"
)

// This file is the website download assembly layer: it turns acquired
// GameDetails into the website tasks the transfer engine consumes, and runs the
// two download commands' chains. It renders nothing and never exits — the total
// size and the per-task verdicts travel as structured results the CLI maps onto
// output and exit codes.

// WebsiteDownloadRequest is one batch download run's input. It carries the
// caller's intent explicitly: the type mask is a per-request decision (what
// --type asked for), not a mutation of the run's persistent configuration.
type WebsiteDownloadRequest struct {
	// Products are the games to download, in the selector's reading.
	Products []string
	// RefMode says how Products are read: by slug, or as --regex selects.
	RefMode ProductRefMode
	// Include overrides the run's type mask for this download when set. Its
	// only producer is --type on the batch leaf; nil means the configured
	// mask. The same resolution feeds the acquisition and the queue, so a
	// category run filters both faces identically.
	Include *uint32
}

// WebsiteTaskFailure is one task's operational failure: what happened, and
// where. The event stream already showed the text; the record exists so the
// aggregate exit code never depends on re-reading messages.
type WebsiteTaskFailure struct {
	Destination string
	Gamename    string
	Err         error
}

// WebsiteDownloadResult is the batch run's structured answer.
type WebsiteDownloadResult struct {
	// TotalSize is the sum of the queue's API-reported sizes, unparsable forms
	// counting as zero — the number behind the CLI's "Total size" line.
	TotalSize int64
	// Tasks is the queue length.
	Tasks int
	// Failures lists every task that ended in an operational failure.
	// Empty means the run may exit zero: successes and the skips the
	// worker semantics authorise.
	Failures []WebsiteTaskFailure
	// DownlinkFailures names the TOP-LEVEL products whose acquisition
	// answered "no files" because every one of their downlink resolutions
	// failed — the empty queue that is an accident, not an answer. Kept DLCs
	// can never appear here (a kept entry resolved at least one file); a
	// discarded DLC's evidence lives in its parent's record.
	DownlinkFailures []string
	// Skipped lists the files the run did not download because they already
	// looked current, each with the evidence that authorised the skip. The
	// batch chain fills it only with remote XML disabled, where "what
	// justified this skip" is a question the front end must answer honestly.
	Skipped []WebsiteSkippedTask
	// Saved is the save-* write side's ledger: every artifact the run
	// evaluated, base artifacts first, then each DLC. ArtifactFailed entries
	// join the aggregate verdict; the rest render.
	Saved []SavedArtifact
	// Notices carries the run's non-progress messages (blacklist
	// diagnostics) for the front end to render.
	Notices []Notice
}

// Failed reports whether any task ended in an operational failure. Artifact
// failures count too — a requested save that could not be written is not a
// success.
func (r WebsiteDownloadResult) Failed() bool {
	if len(r.Failures) > 0 {
		return true
	}
	if len(r.DownlinkFailures) > 0 {
		return true
	}
	for _, a := range r.Saved {
		if a.Action == ArtifactFailed {
			return true
		}
	}
	return false
}

// appendDownlinkNotices records every resolution summary the acquisition left
// behind, recursing into the DLC subtree: "why is there nothing to download"
// has a DLC's failed files in its answer. The failure verdict is top-level
// only — a kept DLC resolved at least one file, and a discarded one was
// absorbed into its parent — so this walk adds no verdict, only notices.
func appendDownlinkNotices(notices []Notice, gd *gamedetails.GameDetails) []Notice {
	if gd.Downlink != nil {
		notices = append(notices, Notice{Text: gd.Gamename + ": " + gd.Downlink.Summary()})
	}
	for i := range gd.DLCs {
		notices = appendDownlinkNotices(notices, &gd.DLCs[i])
	}
	return notices
}

// DownloadWebsite builds and runs the website queue of the named games, with
// the --save-* section but without the XML creation queue.
//
// The games are an explicit selection: there is no account-wide default and no
// --all escape hatch. Acquisition keeps its complete-or-nothing contract; the
// transfer run is per-task: a failure neither cancels the queue nor hides
// itself from the aggregate verdict.
func (d *Downloader) DownloadWebsite(ctx context.Context, req WebsiteDownloadRequest) (WebsiteDownloadResult, error) {
	if len(req.Products) == 0 {
		return WebsiteDownloadResult{}, errors.New("download: no games selected")
	}

	// One resolution rule, one request input, two consumers: the queue
	// resolves here, the acquisition resolves inside GameDetails from the
	// same (req.Include, cfg) pair. The two must never draw from different
	// include sources — that req/cfg split is the silent-filter failure mode
	// this request shape exists to prevent.
	mask := effectiveInclude(req.Include, d.cfg.DownloadConfig.Include)
	details, err := d.GameDetails(ctx, GameDetailsRequest{Products: req.Products, RefMode: req.RefMode, Include: req.Include})
	if err != nil {
		return WebsiteDownloadResult{}, err
	}

	var (
		tasks            []model.WebsiteTask
		notices          []Notice
		saved            []SavedArtifact
		downlinkFailures []string
	)
	bl, err := blacklist.LoadBlacklist(d.cfg.BlacklistFilePath)
	if err != nil {
		return WebsiteDownloadResult{}, err
	}
	for _, line := range bl.Diagnostics() {
		notices = append(notices, Notice{Text: line})
	}
	for i := range details {
		details[i].MakeFilepaths(d.cfg.Directories)
		// The save section runs per game BEFORE the queue grows: a flagged
		// artifact is written even when the transfer later fails, and vice
		// versa.
		saved = append(saved, d.saveGameArtifacts(ctx, &details[i])...)
		// The acquisition's downlink record rides out with the run whether or
		// not the queue grows: every summary the entry (or a kept DLC)
		// recorded becomes a notice, and a top-level full failure is a
		// verdict, not just a message.
		notices = appendDownlinkNotices(notices, &details[i])
		if details[i].Downlink.FullFailure() {
			downlinkFailures = append(downlinkFailures, details[i].Gamename)
		}
		for _, gf := range details[i].GetGameFileVectorFiltered(mask) {
			tasks = append(tasks, websiteTaskFor(gf))
		}
	}

	res := WebsiteDownloadResult{Tasks: len(tasks), Notices: notices, Saved: saved, DownlinkFailures: downlinkFailures}
	for _, t := range tasks {
		res.TotalSize += parseWebsiteSize(t.Size)
	}
	if len(tasks) == 0 {
		// Nothing to fetch is an answer, not a failure — but only when the
		// acquisition recorded no full downlink failure: the res assembled
		// above already carries the verdict, so this return is honest.
		return res, nil
	}

	// The free-space gate returns the error and lets the front end map it, as
	// the install path does. The root is the download directory itself — the
	// destinations are built from it and may not exist yet.
	if d.cfg.DownloadConfig.FreeSpaceCheck {
		if volume, ok := nearestExistingDir(d.cfg.Directories.Directory); ok {
			available, err := freeSpaceAvailable(volume)
			if err != nil {
				return res, err
			}
			if available < uint64(res.TotalSize) {
				return res, fmt.Errorf("not enough free space in %s (%s)",
					filepath.Clean(volume), util.SizeString(available, d.cfg.UnitFormat))
			}
		}
	}

	agg := &websiteAggregate{}
	runErr := d.runWebsiteTasks(ctx, tasks, checksumGated, bl.IsBlacklisted, agg)
	res.Failures = agg.failures
	res.Skipped = agg.skipped
	if runErr != nil {
		return res, runErr
	}
	return res, nil
}

// WebsiteFileOutcome is one download file spec's verdict. Err is nil for a
// success and for the worker-authorised skips.
type WebsiteFileOutcome struct {
	Spec        string
	Destination string
	Err         error
}

// WebsiteFileResult is the aggregate answer of the download file chain.
type WebsiteFileResult struct {
	Outcomes []WebsiteFileOutcome
}

// Failed reports whether any spec ended in an operational failure.
func (r WebsiteFileResult) Failed() bool {
	for _, o := range r.Outcomes {
		if o.Err != nil {
			return true
		}
	}
	return false
}

// DownloadWebsiteFiles runs every spec independently and to the end, then hands
// back the per-spec verdicts. A spec's failure never cancels the others and
// never hides from the aggregate; the front end maps "any failure" onto exit 1
// and keeps the successful downloads.
func (d *Downloader) DownloadWebsiteFiles(ctx context.Context, specs []string, outputFile string, mode ProductRefMode) WebsiteFileResult {
	res := WebsiteFileResult{Outcomes: make([]WebsiteFileOutcome, 0, len(specs))}
	for _, spec := range specs {
		dest, err := d.downloadWebsiteFile(ctx, spec, outputFile, mode)
		res.Outcomes = append(res.Outcomes, WebsiteFileOutcome{Spec: spec, Destination: dest, Err: err})
	}
	return res
}

// downloadWebsiteFile is one spec's chain: parse, acquire, look the id up in
// the recursive vector, run the single task. The return is the destination
// when it became known, whatever the verdict.
func (d *Downloader) downloadWebsiteFile(ctx context.Context, spec, outputFile string, mode ProductRefMode) (string, error) {
	game, dlc, fileid, err := parseWebsiteFileSpec(spec)
	if err != nil {
		return "", err
	}

	all := config.IncludeAllMask()
	details, err := d.GameDetails(ctx, GameDetailsRequest{Products: []string{game}, Include: &all, RefMode: mode})
	if err != nil {
		return "", err
	}

	for i := range details {
		details[i].MakeFilepaths(d.cfg.Directories)
		for _, gf := range details[i].GetGameFileVector() {
			if gf.ID != fileid {
				continue
			}
			if dlc != "" && gf.Gamename != dlc {
				continue
			}
			task := websiteTaskFor(gf)
			if outputFile != "" {
				// -o overrides the derived destination, which is built from
				// the registered layout exactly as the batch chain builds it.
				task.Destination = outputFile
			}
			// The single-file chain reads checksum documents on presence
			// (checksumAlways) and does not consult the blacklist.
			agg := &websiteAggregate{}
			if err := d.runWebsiteTasks(ctx, []model.WebsiteTask{task}, checksumAlways, nil, agg); err != nil {
				return task.Destination, err
			}
			if len(agg.failures) > 0 {
				return task.Destination, agg.failures[0].Err
			}
			return task.Destination, nil
		}
	}

	missing := "file id " + fileid
	if dlc != "" {
		missing = "dlc gamename " + dlc + " / file id " + fileid
	}
	return "", fmt.Errorf("Failed to find file info (gamename: %s / %s)", game, missing)
}

// parseWebsiteFileSpec splits a spec into game, optional DLC gamename and file
// id, after stripping the protocol prefix. Two parts name a base-game file,
// three name a DLC file; anything else is refused. Empty segments are refused
// too: dropping one silently would change which file is meant.
func parseWebsiteFileSpec(spec string) (game, dlc, fileid string, err error) {
	raw := strings.TrimPrefix(spec, config.ProtocolPrefix)
	parts := strings.Split(raw, "/")
	if slices.Contains(parts, "") {
		return "", "", "", fmt.Errorf("invalid file id %q: empty segment", spec)
	}
	switch len(parts) {
	case 2:
		return parts[0], "", parts[1], nil
	case 3:
		return parts[0], parts[1], parts[2], nil
	default:
		return "", "", "", fmt.Errorf("invalid file id %q: could not find separator %q", spec, "/")
	}
}

// websiteTaskFor maps one GameFile onto the transfer task. Checksums belong to
// installers and patches, the size matrix to extras.
func websiteTaskFor(gf gamedetails.GameFile) model.WebsiteTask {
	return model.WebsiteTask{
		Destination: gf.GetFilepath(),
		DownlinkURL: gf.GalaxyDownlinkJSONURL,
		Gamename:    gf.Gamename,
		Size:        gf.Size,
		Checksummed: gf.Type&config.GFInstaller != 0 || gf.Type&config.GFPatch != 0,
		Extra:       gf.Type&config.GFExtra != 0,
	}
}

// parseWebsiteSize reads the API-reported size string: an unparsable form
// counts as zero. A negative form contributes nothing to the total — it could
// not make the free-space gate stricter than "no size".
func parseWebsiteSize(size string) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(size), 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// WebsiteSkippedTask is one file the run skipped as already current, with the
// evidence behind that verdict. The distinction matters to the caller: a
// manifest match means a cached checksum document agreed with the file, while
// a size-only match means nothing looked inside the file at all.
type WebsiteSkippedTask struct {
	Destination string
	Gamename    string
	Evidence    transfer.SkipEvidence
}

// websiteAggregate collects the run's verdicts. The batch chain drives it from
// every download worker, so both ledgers sit behind one mutex — the callbacks
// arrive on the worker goroutines, and an unsynchronised append here would race
// the moment a queue runs wider than one thread.
type websiteAggregate struct {
	mu       sync.Mutex
	failures []WebsiteTaskFailure
	skipped  []WebsiteSkippedTask
}

func (a *websiteAggregate) record(task model.WebsiteTask, err error) {
	if err == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.failures = append(a.failures, WebsiteTaskFailure{
		Destination: task.Destination,
		Gamename:    task.Gamename,
		Err:         err,
	})
}

func (a *websiteAggregate) recordSkip(task model.WebsiteTask, evidence transfer.SkipEvidence) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.skipped = append(a.skipped, WebsiteSkippedTask{
		Destination: task.Destination,
		Gamename:    task.Gamename,
		Evidence:    evidence,
	})
}

// runWebsiteTasks is the one place a website task list reaches the transfer
// layer, so both chains publish the same events through the same observer
// seam as the install run (the runTransfer precedent).
func (d *Downloader) runWebsiteTasks(ctx context.Context, tasks []model.WebsiteTask, policy checksumPolicy, blacklistFn func(string) bool, agg *websiteAggregate) error {
	deps := transfer.WebsiteDeps{
		HTTP:              d.http,
		URL:               d.websiteProvider(policy),
		Observer:          d.transferObserver(),
		Blacklist:         blacklistFn,
		XMLDirectory:      d.cfg.XMLDirectory,
		RemoteXML:         d.cfg.DownloadConfig.RemoteXML,
		TrustAPIForExtras: d.cfg.TrustAPIForExtras,
		SizeOnly:          d.cfg.SizeOnly,
		TaskResult:        agg.record,
	}
	// The automatic manifest and the skip ledger belong to the batch chain:
	// --create-xml is a backup download option, and the skip evidence is what
	// the batch result renders after the frame. The single-file chain reports
	// one spec's verdict and has neither.
	if policy == checksumGated {
		deps.CreateXML = d.cfg.DownloadConfig.CreateXML
		deps.ChunkSize = d.cfg.DownloadConfig.ChunkSize
		deps.SkipReport = agg.recordSkip
	}
	return transfer.RunWebsite(ctx, tasks, d.transferOptions(), deps)
}

// websiteProvider builds the URL seam for one run: the batch chain gets the
// gated checksum policy, the single-file chain gets the always policy.
func (d *Downloader) websiteProvider(policy checksumPolicy) *websiteURLProvider {
	return &websiteURLProvider{
		galaxy:    d.galaxy,
		remoteXML: d.cfg.DownloadConfig.RemoteXML,
		policy:    policy,
		refresh: tokenRefresher{
			refresh: d.refreshAndSave,
			expired: func() bool { return d.token.Expired() },
		},
	}
}
