package transfer

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/nekrozis/goggo/internal/httpx"
	"github.com/nekrozis/goggo/internal/model"
)

// ErrEmptyDownlink and ErrNoDownlink are the two downlink responses the website
// worker skips: an empty document, and one without a "downlink" member. Both
// are warnings, and their text is the message the front end sees.
var (
	ErrEmptyDownlink = errors.New("Empty JSON response, skipping file")
	ErrNoDownlink    = errors.New("Invalid JSON response, skipping file")
)

// downloadFailureKind says what the cleanup step does with a partial file after
// a failed attempt.
type downloadFailureKind uint8

const (
	failureKeep   downloadFailureKind = iota // transport break ⇒ keep the partial file
	failureRemove                            // any other failure ⇒ remove it
)

// downloadError is one download attempt's failure. keep decides the cleanup
// (keep the partial file vs remove it); retryable decides whether another
// attempt may follow. The two are independent: a transport failure keeps the
// file and may retry, a local I/O failure removes it and never retries.
type downloadError struct {
	err       error
	kind      downloadFailureKind
	retryable bool
}

// WebsiteURLProvider resolves a website task's download url through the Galaxy
// API: the downlink JSON document carries the "downlink" url and, for
// installers and patches, a "checksum" url whose document holds the md5 the
// version check compares against. Concurrency: RunWebsite's workers call
// Resolve concurrently, so an implementation must be safe for concurrent use.
type WebsiteURLProvider interface {
	Resolve(ctx context.Context, task model.WebsiteTask) (downlinkURL, checksumXML string, err error)
}

// WebsiteDeps carries everything the website run needs from outside. The flag
// fields are explicit values, not configuration reads; the XML directory is
// where the remote checksum documents are cached.
type WebsiteDeps struct {
	HTTP              *httpx.Client
	URL               WebsiteURLProvider
	Observer          Observer
	Blacklist         func(path string) bool // nil disables the per-file filter
	XMLDirectory      string
	RemoteXML         bool
	TrustAPIForExtras bool
	SizeOnly          bool
	// TaskResult, when set, receives the run's terminal outcome for every
	// task: nil for a success and for each skip the worker semantics authorise,
	// non-nil for an operational failure. The event stream carries the same
	// facts as messages; this seam exists because a message kind is not a
	// result code, so counting failures must not infer from an event sequence.
	TaskResult func(task model.WebsiteTask, err error)
}

// RunWebsite executes the website download path as upstream writes it: the
// single-file downloads with their version checks, the rename of the old file,
// resume handling, the failure-cleanup matrix and the SSL retry class.
//
// Failure semantics are the website worker's own: per-item problems —
// blacklisted files, missing directories, unusable downlink documents, renames
// that fail — are reported and skipped without failing the run, while a download
// that exhausts its retries is cleaned up according to its failure class.
// RunWebsite returns nil when every task has ended; only a cancelled context makes
// it return an error. The scheduling, the cancellation and the event delivery are
// shared with the Galaxy path.
func RunWebsite(ctx context.Context, tasks []model.WebsiteTask, opts Options, deps WebsiteDeps) error {
	if deps.HTTP == nil || deps.URL == nil || deps.Observer == nil {
		return errors.New("transfer: website run needs an http client, a url provider and an observer")
	}
	return schedule(ctx, tasks, opts.Workers,
		func(ev Event) { deps.Observer.OnEvent(ev) },
		func(ctx context.Context, task model.WebsiteTask, emit func(Event)) error {
			err := runWebsiteTask(ctx, task, opts, deps, emit)
			if deps.TaskResult != nil {
				deps.TaskResult(task, err)
			}
			return err
		})
}

// runWebsiteTask downloads one website file: the blacklist filter, the
// directory check, the downlink document, the version and completeness checks,
// then the download loop. Conditions that merely skip a file return nil;
// conditions that fail a download return the error after emitting the failure
// events.
func runWebsiteTask(ctx context.Context, task model.WebsiteTask, opts Options, deps WebsiteDeps, emit func(Event)) error {
	name := filepath.Base(task.Destination)

	fail := func(text string) error {
		emit(Event{Path: task.Destination, Text: text, Kind: EventMessageError})
		emit(Event{Path: task.Destination, Kind: EventTaskFinish})
		return errors.New(text)
	}
	skip := func(kind EventKind, text string) error {
		emit(Event{Path: task.Destination, Text: text, Kind: kind})
		emit(Event{Path: task.Destination, Kind: EventTaskFinish})
		return nil
	}

	emit(Event{Path: task.Destination, Kind: EventTaskStart})

	// The blacklist filter.
	if deps.Blacklist != nil && deps.Blacklist(task.Destination) {
		return skip(EventMessageInfo, "Blacklisted file: "+task.Destination)
	}

	// Directories: an occupied path skips the file
	// with a warning, a failed creation with an error; both are non-fatal.
	dir := filepath.Dir(task.Destination)
	if fi, err := os.Stat(dir); err == nil {
		if !fi.IsDir() {
			return skip(EventMessageWarning, dir+" is not directory, skipping file ("+name+")")
		}
	} else if err := os.MkdirAll(dir, 0o755); err != nil {
		return skip(EventMessageError, "Failed to create directory ("+dir+"), skipping file ("+name+")")
	}

	// The downlink document.
	downlink, checksumXML, err := deps.URL.Resolve(ctx, task)
	if err != nil {
		switch {
		case errors.Is(err, ErrEmptyDownlink):
			return skip(EventMessageWarning, ErrEmptyDownlink.Error())
		case errors.Is(err, ErrNoDownlink):
			return skip(EventMessageWarning, ErrNoDownlink.Error())
		}
		return fail(err.Error())
	}

	// The version and completeness checks.
	fileExists := regularFileExists(task.Destination)
	bSameVersion := true
	bIsComplete := false
	var filesizeXML int64

	apiSize, _ := strconv.ParseInt(strings.TrimSpace(task.Size), 10, 64)

	if task.Checksummed && deps.RemoteXML && checksumXML != "" {
		localHash := localFileHash(deps.XMLDirectory, task.Destination, task.Gamename)
		// SizeOnly fetches the document but skips the comparison.
		if localHash != "" && !deps.SizeOnly {
			remoteMD5, remoteSize, perr := parseFileXML(checksumXML)
			if perr == nil {
				filesizeXML = remoteSize
				if remoteMD5 != localHash {
					bSameVersion = false
				}
			}
		}
	}

	if task.Extra && fileExists {
		filesizeLocal := fileSize(task.Destination)
		if bLocalXMLExists := localXMLExists(deps.XMLDirectory, task.Gamename, name); bLocalXMLExists {
			if data, err := os.ReadFile(localXMLPath(deps.XMLDirectory, task.Gamename, name)); err == nil {
				if _, total, perr := parseFileXML(string(data)); perr == nil {
					filesizeXML = total
				}
			}
		}

		// The API is not trusted for extras unless asked: the comparison size
		// comes from a content-length probe instead.
		var filesizeCompare int64
		if deps.TrustAPIForExtras {
			filesizeCompare = apiSize
		} else {
			filesizeCompare = contentLength(ctx, deps.HTTP, downlink)
		}

		bLocalAssumedComplete := filesizeXML > 0 && filesizeLocal == filesizeXML
		if bLocalAssumedComplete {
			bSameVersion = filesizeLocal == filesizeCompare
			if bSameVersion {
				bIsComplete = true
			}
		} else {
			if filesizeLocal == filesizeCompare {
				bIsComplete = true
				bSameVersion = true
			} else {
				// Assume same version while the local file is smaller than the
				// remote one.
				bSameVersion = filesizeLocal < filesizeCompare
			}
		}
	}

	if bIsComplete {
		emit(Event{Path: task.Destination, Text: "Skipping complete file: " + name, Kind: EventMessageInfo})
	}

	// Resume or rename.
	bResume := false
	if fileExists && !bIsComplete {
		if bSameVersion {
			bResume = true
			// A checksum document also carries the total size: a local file of
			// exactly that size is complete, not partial.
			if checksumXML != "" {
				if fi, err := os.Stat(task.Destination); err == nil {
					if _, total, perr := parseFileXML(checksumXML); perr == nil && fi.Size() == total {
						emit(Event{Path: task.Destination, Text: "Skipping complete file: " + name, Kind: EventMessageInfo})
						bIsComplete = true
					}
				}
			}
		} else {
			emit(Event{Path: task.Destination, Text: "Remote file is different, renaming local file", Kind: EventMessageInfo})
			newName := task.Destination + "." + time.Now().Format("20060102T150405") + ".old"
			if err := os.Rename(task.Destination, newName); err != nil {
				return skip(EventMessageWarning, "Failed to rename "+task.Destination+" to "+newName+" - Skipping file")
			}
			fileExists = false
		}
	}

	// Save the remote checksum document.
	if checksumXML != "" {
		bLocalXMLExists := localXMLExists(deps.XMLDirectory, task.Gamename, name)
		if !bLocalXMLExists || (bLocalXMLExists && !bSameVersion) {
			xmlDir := filepath.Join(deps.XMLDirectory, task.Gamename)
			if err := os.MkdirAll(xmlDir, 0o755); err != nil {
				emit(Event{Path: task.Destination, Text: "Failed to create directory: " + xmlDir, Kind: EventMessageError})
			} else if err := os.WriteFile(filepath.Join(xmlDir, name+".xml"), []byte(checksumXML), 0o644); err != nil {
				emit(Event{Path: task.Destination, Text: "Can't create " + filepath.Join(xmlDir, name+".xml"), Kind: EventMessageError})
			}
		}
	}

	// A complete file is skipped once its xml data is saved.
	if bIsComplete {
		emit(Event{Path: task.Destination, Kind: EventTaskFinish})
		return nil
	}

	// The download loop — the attempt/retry/cleanup contract shared with the
	// direct artifact download.
	return downloadWithRetries(ctx, task, opts, deps, downlink, bResume, emit)
}

// downloadWithRetries is the ONE attempt loop: retry classification, the
// resume handoff between attempts, the success timestamp and the failure
// cleanup live here once. RunWebsite's tasks and DownloadArtifact's logos and
// icons both run through it, so the artifact path synthesizes a task carrying
// only its destination rather than adding a second download implementation.
func downloadWithRetries(ctx context.Context, task model.WebsiteTask, opts Options, deps WebsiteDeps, downlink string, bResume bool, emit func(Event)) error {
	name := filepath.Base(task.Destination)
	var lastErr error
	var lastKeep bool
	var reason string
	lastResume := false
	for attempt := 0; ; attempt++ {
		if opts.Wait > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(opts.Wait):
			}
		}
		if attempt > 0 {
			emit(Event{Path: task.Destination,
				Text: fmt.Sprintf("Retry %d/%d: %s (%s)", attempt, opts.Retries, filepath.Base(task.Destination), reason),
				Kind: EventMessageInfo})
		}

		lastModified, derr := websiteDownloadAttempt(ctx, task, deps, downlink, bResume, emit)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if derr == nil {
			// Success: the server's timestamp moves
			// onto the file, then the completion message. The rate suffix is
			// rendered by the progress step.
			if !lastModified.IsZero() {
				if err := os.Chtimes(task.Destination, lastModified, lastModified); err != nil {
					emit(Event{Path: task.Destination, Text: err.Error(), Kind: EventMessageWarning})
				}
			}
			emit(Event{Path: task.Destination, Text: "Download complete: " + name, Kind: EventMessageSuccess})
			emit(Event{Path: task.Destination, Kind: EventTaskFinish})
			return nil
		}
		lastErr = derr.err
		lastKeep = derr.kind == failureKeep
		// The retry budget mirrors the galaxy path: Retries retries on top of
		// the initial attempt.
		if !derr.retryable || attempt >= opts.Retries {
			break
		}
		// Whatever landed on disk makes the next attempt a resume.
		if regularFileExists(task.Destination) {
			bResume = true
			lastResume = true
		}
		reason = lastErr.Error()
	}

	// The failure cleanup: the message, then the
	// partial file's fate — a transport break or a resume attempt keeps it,
	// any other failure removes it, and a zero-length file always goes.
	emit(Event{Path: task.Destination, Text: "Download complete (" + lastErr.Error() + "): " + name, Kind: EventMessageWarning})
	if fi, err := os.Stat(task.Destination); err == nil && fi.Mode().IsRegular() {
		if (!lastKeep && !lastResume) || fi.Size() == 0 {
			if err := os.Remove(task.Destination); err != nil {
				emit(Event{Path: task.Destination, Text: "Failed to delete " + name, Kind: EventMessageError})
			}
		}
	}
	emit(Event{Path: task.Destination, Kind: EventTaskFinish})
	// The exhausted retries are this task's operational failure: the events
	// say what happened, the verdict rides on the return value (the
	// documented contract of this function, and what RunWebsite's TaskResult
	// seam reports for the aggregate exit code).
	return lastErr
}

// websiteDownloadAttempt performs one attempt of the file download. A nil
// downloadError means the attempt succeeded. The failure classification happens
// where each error is produced — network-side failures keep the partial file,
// local-side failures remove it.
func websiteDownloadAttempt(ctx context.Context, task model.WebsiteTask, deps WebsiteDeps, url string, resume bool, emit func(Event)) (lastModified time.Time, derr *downloadError) {
	var f *os.File
	var err error
	if resume {
		// Resume opens without truncation; the Range header moves the server's
		// start to what is already on disk.
		f, err = os.OpenFile(task.Destination, os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return time.Time{}, &downloadError{fmt.Errorf("Failed to open %s: %w", task.Destination, err), failureRemove, false}
		}
	} else {
		f, err = os.OpenFile(task.Destination, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
		if err != nil {
			return time.Time{}, &downloadError{fmt.Errorf("Failed to create %s: %w", task.Destination, err), failureRemove, false}
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		f.Close()
		return time.Time{}, &downloadError{httpx.SanitizeError(err), failureRemove, false}
	}
	if resume {
		req.Header.Set("Range", "bytes="+strconv.FormatInt(fileSize(task.Destination), 10)+"-")
	}
	resp, err := deps.HTTP.Do(ctx, req)
	if err != nil {
		f.Close()
		// A transport failure mid-transfer is the PARTIAL_FILE/TIMEDOUT class:
		// the partial file stays and another attempt may follow.
		return time.Time{}, &downloadError{err, failureKeep, true}
	}
	defer resp.Body.Close()

	// A 416 on a resume means the file on disk is already complete, and is
	// treated as success. On a fresh download it is a failure whose empty file
	// the cleanup removes.
	if resp.StatusCode == http.StatusRequestedRangeNotSatisfiable && resume {
		f.Close()
		return lastModifiedFrom(resp), nil
	}
	if resp.StatusCode >= 400 {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		f.Close()
		return time.Time{}, &downloadError{
			httpx.NewStatusError(http.MethodGet, url, resp.StatusCode),
			failureRemove, resp.StatusCode != http.StatusRequestedRangeNotSatisfiable,
		}
	}

	// The stream is read and written block by block so that a read error (the
	// network side) and a write error (the local side) classify separately —
	// an io.Copy error could be either.
	buf := make([]byte, 64<<10)
	for {
		if ctx.Err() != nil {
			f.Close()
			return time.Time{}, &downloadError{ctx.Err(), failureKeep, false}
		}
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				f.Close()
				return time.Time{}, &downloadError{werr, failureRemove, false}
			}
		}
		if errors.Is(rerr, io.EOF) {
			break
		}
		if rerr != nil {
			f.Close()
			return time.Time{}, &downloadError{rerr, failureKeep, true}
		}
	}
	if err := f.Close(); err != nil {
		return time.Time{}, &downloadError{err, failureRemove, false}
	}
	return lastModifiedFrom(resp), nil
}

// lastModifiedFrom parses the response's Last-Modified header, if any.
func lastModifiedFrom(resp *http.Response) time.Time {
	if lm := resp.Header.Get("Last-Modified"); lm != "" {
		if t, err := http.ParseTime(lm); err == nil {
			return t
		}
	}
	return time.Time{}
}

// localFileHash returns the local file's md5: the md5 stored in the cached
// checksum document wins when that document exists, otherwise the file's own
// md5 is computed. A document without an md5 attribute yields "" without
// falling back to computing the hash.
func localFileHash(xmlDir, dest, gamename string) string {
	localXML := localXMLPath(xmlDir, gamename, dest)
	if _, err := os.Stat(localXML); err == nil {
		if data, err := os.ReadFile(localXML); err == nil {
			var doc struct {
				MD5 string `xml:"md5,attr"`
			}
			if xml.Unmarshal(data, &doc) == nil {
				return doc.MD5
			}
			return ""
		}
		return ""
	}
	f, err := os.Open(dest)
	if err != nil {
		return ""
	}
	defer f.Close()
	sum := md5.New()
	if _, err := io.Copy(sum, f); err != nil {
		return ""
	}
	return hex.EncodeToString(sum.Sum(nil))
}

// localXMLPath is the cached checksum document of one file:
// <dir>/<gamename>/<name>.xml, or <dir>/<name>.xml without a game name.
func localXMLPath(xmlDir, gamename, dest string) string {
	name := filepath.Base(dest) + ".xml"
	if gamename == "" {
		return filepath.Join(xmlDir, name)
	}
	return filepath.Join(xmlDir, gamename, name)
}

// localXMLExists reports whether the cached checksum document exists.
func localXMLExists(xmlDir, gamename, dest string) bool {
	_, err := os.Stat(localXMLPath(xmlDir, gamename, dest))
	return err == nil
}

// parseFileXML reads the md5 and total_size attributes of a checksum document
// ("<file md5="…" total_size="…"/>"). An unparsable size counts as 0.
func parseFileXML(data string) (md5hex string, totalSize int64, err error) {
	var doc struct {
		MD5       string `xml:"md5,attr"`
		TotalSize string `xml:"total_size,attr"`
	}
	if err := xml.Unmarshal([]byte(data), &doc); err != nil {
		return "", 0, err
	}
	n, perr := strconv.ParseInt(strings.TrimSpace(doc.TotalSize), 10, 64)
	if perr != nil {
		n = 0
	}
	return doc.MD5, n, nil
}

// contentLength probes the download url's content length with a HEAD request. A
// failed probe counts as zero.
func contentLength(ctx context.Context, hx *httpx.Client, url string) int64 {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return 0
	}
	resp, err := hx.Do(ctx, req)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	return resp.ContentLength
}

// fileSize returns a regular file's size, or 0 when it cannot be stat'd.
func fileSize(path string) int64 {
	if fi, err := os.Stat(path); err == nil && fi.Mode().IsRegular() {
		return fi.Size()
	}
	return 0
}

// regularFileExists reports whether a regular file exists at path.
func regularFileExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular()
}
