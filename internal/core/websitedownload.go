package core

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/nekrozis/goggo/internal/blacklist"
	"github.com/nekrozis/goggo/internal/config"
	"github.com/nekrozis/goggo/internal/gamedetails"
	"github.com/nekrozis/goggo/internal/model"
	"github.com/nekrozis/goggo/internal/transfer"
	"github.com/nekrozis/goggo/internal/util"
)

// This file is GD4's assembly layer: it turns acquired GameDetails into the
// website tasks the already-ported transfer engine consumes, and runs the two
// download commands' chains. It renders nothing and never exits - Total size
// and per-task verdicts travel as structured results the CLI maps onto output
// and exit codes (GD4 Gate 1 rulings 6-8).

// websiteProtocolPrefix mirrors GlobalConstants::PROTOCOL_PREFIX
// (globalconstants.h:31); download file specs may carry it and lose it.
const websiteProtocolPrefix = "gogdownloader://"

// WebsiteTaskFailure is one task's operational failure: what happened, and
// where. The event stream already showed the text; the record exists so the
// aggregate exit code never depends on re-reading messages (GD4 3.4).
type WebsiteTaskFailure struct {
	Destination string
	Gamename    string
	Err         error
}

// WebsiteDownloadResult is the batch run's structured answer.
type WebsiteDownloadResult struct {
	// TotalSize is the sum of the queue's API-reported sizes, unparsable
	// forms counting as zero - the number behind the CLI's "Total size"
	// line, upstream's own accumulator (downloader.cpp:771-783).
	TotalSize int64
	// Tasks is the queue length.
	Tasks int
	// Failures lists every task that ended in an operational failure.
	// Empty means the run may exit zero: successes and the skips the
	// worker semantics authorise (GD4 aggregate exit contract).
	Failures []WebsiteTaskFailure
	// Saved is the GD5 write side's ledger: every save-* artifact the run
	// evaluated, in upstream order (base artifacts first, then each DLC).
	// ArtifactFailed entries join the aggregate verdict; the rest render.
	Saved []SavedArtifact
	// Notices carries the run's non-progress messages (blacklist
	// diagnostics) for the front end to render.
	Notices []Notice
}

// Failed reports whether any task ended in an operational failure. Artifact
// failures count too — a requested save that could not be written is not a
// success (GD5 Gate 1 approval: continue per item, never hide a failure).
func (r WebsiteDownloadResult) Failed() bool {
	if len(r.Failures) > 0 {
		return true
	}
	for _, a := range r.Saved {
		if a.Action == ArtifactFailed {
			return true
		}
	}
	return false
}

// DownloadWebsite builds and runs the website queue of the named games
// (Downloader::download, downloader.cpp:682-840, without the --save-* section
// that belongs to GD5 and without the XML creation queue that belongs to
// XML1 - the plan's scope ruling 4).
//
// The games are an explicit selection: there is no account-wide default and
// no --all escape hatch (GD4 Gate 1 ruling 2). Acquisition keeps GD3's
// complete-or-nothing contract; the transfer run is per-task: a failure
// neither cancels the queue nor hides itself from the aggregate verdict
// (ruling 5 and the aggregate exit contract).
func (d *Downloader) DownloadWebsite(ctx context.Context, products []string) (WebsiteDownloadResult, error) {
	if len(products) == 0 {
		return WebsiteDownloadResult{}, errors.New("download: no games selected")
	}

	details, err := d.GameDetails(ctx, GameDetailsRequest{Products: products})
	if err != nil {
		return WebsiteDownloadResult{}, err
	}

	var (
		tasks   []model.WebsiteTask
		notices []Notice
		saved   []SavedArtifact
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
		// The save section runs per game BEFORE the queue grows, the upstream
		// order (downloader.cpp:694-760 precedes 762-778): a flagged artifact
		// is written even when the transfer later fails, and vice versa.
		saved = append(saved, d.saveGameArtifacts(ctx, &details[i])...)
		for _, gf := range details[i].GetGameFileVectorFiltered(d.cfg.DownloadConfig.Include) {
			tasks = append(tasks, websiteTaskFor(gf))
		}
	}

	res := WebsiteDownloadResult{Tasks: len(tasks), Notices: notices, Saved: saved}
	for _, t := range tasks {
		res.TotalSize += parseWebsiteSize(t.Size)
	}
	if len(tasks) == 0 {
		// Nothing to fetch is an answer, not a failure: upstream's empty
		// queue skips the run entirely (downloader.cpp:779).
		return res, nil
	}

	// The free-space gate (downloader.cpp:785-802): upstream exits, this
	// port returns the error and lets the front end map it (GD4 ruling 6,
	// the install precedent). The root is the download directory itself -
	// the destinations are built from it and may not exist yet.
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

// DownloadWebsiteFiles runs every spec independently and to the end, then
// hands back the per-spec verdicts (the chain of downloadFileWithId,
// downloader.cpp:2382-2526, dispatched per spec the way main.cpp:820 runs
// them). A spec's failure never cancels the others and never hides from the
// aggregate (GD4 Gate 1 rulings 5 and 8); the front end maps "any failure"
// onto exit 1 and keeps the successful downloads.
func (d *Downloader) DownloadWebsiteFiles(ctx context.Context, specs []string, outputFile string) WebsiteFileResult {
	res := WebsiteFileResult{Outcomes: make([]WebsiteFileOutcome, 0, len(specs))}
	for _, spec := range specs {
		dest, err := d.downloadWebsiteFile(ctx, spec, outputFile)
		res.Outcomes = append(res.Outcomes, WebsiteFileOutcome{Spec: spec, Destination: dest, Err: err})
	}
	return res
}

// downloadWebsiteFile is one spec's chain: parse, acquire, look the id up in
// the recursive vector, run the single task. The return is the destination
// when it became known, whatever the verdict.
func (d *Downloader) downloadWebsiteFile(ctx context.Context, spec, outputFile string) (string, error) {
	game, dlc, fileid, err := parseWebsiteFileSpec(spec)
	if err != nil {
		return "", err
	}

	all := config.IncludeAllMask()
	details, err := d.GameDetails(ctx, GameDetailsRequest{Products: []string{game}, Include: &all})
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
				// -o overrides the derived destination. Upstream's help
				// text claims the subdir options are ignored; the code
				// shows the destination comes from getFilepath either way
				// (downloader.cpp:2521-2524), so this chain honours the
				// registered layout like the batch one.
				task.Destination = outputFile
			}
			// The single-file chain reads checksum documents on presence
			// (checksumAlways) and does not consult the blacklist - both
			// are downloadFileWithId's own semantics, not the worker's
			// (GD4 3.5, downloader.cpp:2394-2526).
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

// parseWebsiteFileSpec splits a spec into game, optional DLC gamename and
// file id, after stripping the protocol prefix (main.cpp:553-555). Two parts
// name a base-game file, three name a DLC file; anything else is the
// upstream "could not find separator" refusal. Empty segments are refused
// too: the upstream tokenizer would drop them silently, and a silently
// dropped segment changes which file is meant (the CLI1 D2 rule).
func parseWebsiteFileSpec(spec string) (game, dlc, fileid string, err error) {
	raw := strings.TrimPrefix(spec, websiteProtocolPrefix)
	parts := strings.Split(raw, "/")
	for _, p := range parts {
		if p == "" {
			return "", "", "", fmt.Errorf("invalid file id %q: empty segment", spec)
		}
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

// websiteTaskFor maps one GameFile onto the transfer task. The two behaviour
// flags are the batch semantics frozen by the Gate 1 ruling 3: checksums
// belong to installers and patches, the size matrix to extras - whatever the
// real API's documents carry.
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

// parseWebsiteSize reads the API-reported size string the way the worker
// reads it: an unparsable form counts as zero (std::stol's catch,
// downloader.cpp:3090-3092). A negative form contributes nothing to the
// total: it could not make the free-space gate stricter than "no size".
func parseWebsiteSize(size string) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(size), 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// websiteAggregate records the per-task verdicts the TaskResult seam reports.
// It is the run's only result ledger: the CLI renders from it, never from a
// second tally of its own (GD4 Gate 2: one source of truth per result).
type websiteAggregate struct {
	failures []WebsiteTaskFailure
}

func (a *websiteAggregate) record(task model.WebsiteTask, err error) {
	if err != nil {
		a.failures = append(a.failures, WebsiteTaskFailure{
			Destination: task.Destination,
			Gamename:    task.Gamename,
			Err:         err,
		})
	}
}

// runWebsiteTasks is the one place a website task list reaches the transfer
// layer, so both chains publish the same events through the same observer
// seam as the install run (the runTransfer precedent).
func (d *Downloader) runWebsiteTasks(ctx context.Context, tasks []model.WebsiteTask, policy checksumPolicy, blacklistFn func(string) bool, agg *websiteAggregate) error {
	return transfer.RunWebsite(ctx, tasks, d.transferOptions(), transfer.WebsiteDeps{
		HTTP:              d.http,
		URL:               d.websiteProvider(policy),
		Observer:          d.transferObserver(),
		Blacklist:         blacklistFn,
		XMLDirectory:      d.cfg.XMLDirectory,
		RemoteXML:         d.cfg.DownloadConfig.RemoteXML,
		TrustAPIForExtras: d.cfg.TrustAPIForExtras,
		SizeOnly:          d.cfg.SizeOnly,
		TaskResult:        agg.record,
	})
}

// websiteProvider builds the URL seam for one run: the batch chain gets the
// gated checksum policy, the single-file chain gets the always policy
// (GD4 3.5 - two gates, two constructions, one provider).
func (d *Downloader) websiteProvider(policy checksumPolicy) *websiteURLProvider {
	return &websiteURLProvider{
		galaxy:    d.galaxy,
		remoteXML: d.cfg.DownloadConfig.RemoteXML,
		policy:    policy,
		refresh: tokenRefresher{
			refresh: d.refreshAndSave,
			expired: func() bool { return d.token.IsExpired() },
		},
	}
}
