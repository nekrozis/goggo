package transfer

import (
	"strconv"
	"strings"
)

// EventKind tells the front end what kind of thing happened: one kind drives
// the progress bar, the other four map onto ui/log's message types.
type EventKind int

const (
	EventProgress EventKind = iota
	EventMessageInfo
	EventMessageWarning
	EventMessageError
	EventMessageSuccess
	EventTaskStart
	EventTaskFinish
)

// Event is the only thing transfer emits. The front end turns it into a
// ui/log.Message or a ui/progress.Bar fraction; transfer knows neither, and it
// carries no verbosity level — filtering by level is the front end's decision.
//
// ChunkIndex is -1 for file-level events (a run starting or finishing) and
// 0-based inside a chunk loop; ChunkCount is the file's chunk count. Current
// and Total are byte counts, with Current advancing while Total stays the
// file's size. Path is the file the event belongs to and Text is the message
// text of the message kinds, empty for progress.
type Event struct {
	Path       string
	Text       string
	Current    int64
	Total      int64
	ChunkIndex int
	ChunkCount int
	Kind       EventKind
}

// Observer receives transfer events. The front end implements it; transfer
// calls it synchronously and never assumes the call is cheap - an
// implementation that renders a progress bar does I/O on every Progress event.
type Observer interface {
	OnEvent(Event)
}

// ResumeMessagePrefix marks an EventMessageInfo as the explicit resume marker.
// Counting resumed tasks must ride on this signal, never on an inferred event
// sequence: a task that shows no progress events is not necessarily a resume
// (a zero-size item shares that shape), so an inference would embed a fragile
// behavioural guess into the UI.
const ResumeMessagePrefix = "Resuming from chunk "

// ResumeMessage builds the marker text. The absolute path it carries is the
// lifecycle record: diagnostics keep filesystem identity, the presentation
// layer decides what reaches the screen.
func ResumeMessage(startChunk int, path string) string {
	return ResumeMessagePrefix + strconv.Itoa(startChunk) + ": " + path
}

// IsResumeMessage reports whether a message text is the explicit marker.
func IsResumeMessage(text string) bool {
	return strings.HasPrefix(text, ResumeMessagePrefix)
}

// SkipMessagePrefix marks an EventMessageSuccess as transfer's authoritative
// "nothing to transfer". The front end aggregates skips on this explicit signal
// for the same reason it counts resumes on theirs: no event-sequence inference.
const SkipMessagePrefix = "Skipped: "

// SkipMessage builds the skip marker text; the ": OK" tail keeps the message
// reading like the download record it is.
func SkipMessage(path string) string {
	return SkipMessagePrefix + path + ": OK"
}

// IsSkipMessage reports whether a message text is the explicit skip marker.
func IsSkipMessage(text string) bool {
	return strings.HasPrefix(text, SkipMessagePrefix)
}
