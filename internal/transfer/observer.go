package transfer

// EventKind tells the front end what kind of thing happened: one kind drives
// the progress bar, the other four map onto ui/log's message types.
type EventKind int

const (
	EventProgress EventKind = iota
	EventMessageInfo
	EventMessageWarning
	EventMessageError
	EventMessageSuccess
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
//
// Fields are ordered to minimise padding: the strings (16B each) first, then
// the 8B fields.
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
// calls it synchronously and never assumes the call is cheap — an
// implementation that renders a progress bar does I/O on every Progress event.
type Observer interface {
	OnEvent(Event)
}
