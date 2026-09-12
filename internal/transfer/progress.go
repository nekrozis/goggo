package transfer

import (
	"io"
	"sync"
	"sync/atomic"
)

// Progress is the running state of one run, in two layers.
//
// The queue layer is the run-level fact: the tasks Run was handed and their
// summed compressed size, published once before the first dispatch and never
// decremented. The slot layer is the per-task lifecycle state: the bytes a task
// has downloaded and its total, published as the transfer proceeds and dropped
// when the task ends. A reader therefore learns what is still *pending* by
// subtracting what it has seen start from the queue snapshot, and what is
// *active* from the slots — the pending/active split stays explicit and nothing
// is counted twice (review S-ETA3).
//
// The asymmetry between the two layers is deliberate: Queue keeps answering
// after Run returns, while Bytes and Total answer false once a task's slot is
// gone (review S-ETA3).
//
// transfer publishes into it as bytes arrive from the network and the front end
// polls it, the way upstream's curl progress callback writes vDownloadInfo and
// printProgress reads it (downloader.cpp:3445-3499, 3545-3561). It carries no
// front-end concept of its own: no method calls out to a renderer or an
// observer, and the counts are maintained whether or not anyone reads them
// (review S-ETA2).
//
// A nil *Progress is a usable no-op: readers get "no sampling state" and the
// run loop builds no wrapper around the response bodies, so a run without a
// registry behaves exactly as it did before this type existed.
//
// The registry is task-keyed rather than galaxy-specific on purpose — the
// website path can publish into it later — but only the chunk path feeds it
// today (review S-ETA2).
type Progress struct {
	mu    sync.Mutex
	slots map[string]*progressSlot
	queue progressQueue
}

// progressQueue is the run-level queue snapshot: how many tasks the run started
// with and how many compressed bytes they carry. published says whether a
// snapshot exists at all, which is what keeps "no snapshot" distinct from "an
// empty queue" (review S-ETA3).
type progressQueue struct {
	bytes     int64
	tasks     int
	published bool
}

// progressSlot is one task's logical progress. value is written by the task's
// worker goroutine and read by the front end, so it is atomic and neither side
// takes mu on the hot path. total is written once, before the slot becomes
// visible through the registry, and never changes afterwards.
type progressSlot struct {
	value atomic.Int64
	total int64
}

// NewProgress returns an empty registry.
func NewProgress() *Progress {
	return &Progress{slots: make(map[string]*progressSlot)}
}

// Bytes returns the task's logical download progress in bytes and whether the
// task has a sampling slot at all. false means "no sampling state", never
// "zero bytes": a task that has started but read nothing answers (0, true).
func (p *Progress) Bytes(task string) (int64, bool) {
	if p == nil {
		return 0, false
	}
	slot := p.slot(task)
	if slot == nil {
		return 0, false
	}
	return slot.value.Load(), true
}

// Total returns the task's logical total bytes — the same number the progress
// events carry as Total, which is the file's compressed size. It is not a
// chunk size, not a remaining count and not the size of the current response
// body (review S-ETA2).
func (p *Progress) Total(task string) (int64, bool) {
	if p == nil {
		return 0, false
	}
	slot := p.slot(task)
	if slot == nil {
		return 0, false
	}
	// Safe without mu: total is only written before the slot is published.
	return slot.total, true
}

// Queue returns the run-level queue snapshot: how many tasks the last run was
// handed and their summed compressed size. ok is false when no run has
// published a snapshot yet — never "the queue is empty", which is what an empty
// run publishes instead (review S-ETA3).
//
// The snapshot outlives the run: it is a static fact about the queue, while
// Bytes and Total describe a task's lifecycle and stop answering once the task
// ends.
func (p *Progress) Queue() (tasks int, bytes int64, ok bool) {
	if p == nil {
		return 0, 0, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.queue.published {
		return 0, 0, false
	}
	return p.queue.tasks, p.queue.bytes, true
}

// setQueue publishes the queue snapshot. Run calls it once, before the first
// dispatch, so a reader can compute what is still pending from the tasks it has
// seen start (review S-ETA3).
func (p *Progress) setQueue(tasks int, bytes int64) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.queue = progressQueue{tasks: tasks, bytes: bytes, published: true}
	p.mu.Unlock()
}

// slot looks a task up. The caller reads the slot outside the lock, which is
// why the registry never hands out a slot it still mutates.
func (p *Progress) slot(task string) *progressSlot {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.slots[task]
}

// start publishes a fresh slot for the task, carrying its logical total, and
// replaces any earlier one. It returns the slot the worker goroutine publishes
// into; a nil registry returns a nil slot, whose store is a no-op.
func (p *Progress) start(task string, total int64) *progressSlot {
	if p == nil {
		return nil
	}
	slot := &progressSlot{total: total}
	p.mu.Lock()
	p.slots[task] = slot
	p.mu.Unlock()
	return slot
}

// finish drops the task's slot: the task is over, so a reader learns "no
// sampling state" rather than a count that will never move again.
func (p *Progress) finish(task string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	delete(p.slots, task)
	p.mu.Unlock()
}

// store publishes an absolute logical progress value. It is a no-op on the nil
// slot a registry-less run hands down.
func (s *progressSlot) store(value int64) {
	if s == nil {
		return
	}
	s.value.Store(value)
}

// progressSink tells one chunk fetch where to report the bytes it reads. The
// zero value reports nothing, so a run without a Progress keeps the plain
// io.Copy behaviour.
type progressSink struct {
	slot *progressSlot
	base int64 // logical bytes of this attempt's starting point
	end  int64 // the chunk's logical end: offset + compressed size
}

// progressReader publishes the bytes a response body yields as they arrive, so
// the sampler sees a transfer that is still in flight rather than only the
// chunk boundaries the events report. The value is absolute — the attempt's
// base plus everything read so far — which keeps a retry that resumes from the
// bytes already in memory monotone without any accumulator to get wrong, and it
// is bounded by the chunk's logical end: a server that ignores the Range and
// re-sends the whole chunk cannot push the sample past the end, which would
// otherwise turn the caller's 200-fold into an artificial decrease
// (review S-ETA2 R1).
//
// A decrease is still possible and deliberate: when a failed hash discards the
// chunk buffer, the next attempt starts from the chunk's offset again. The
// window that consumes these samples treats any decrease as a restart
// (review S-ETA2).
type progressReader struct {
	r    io.Reader
	slot *progressSlot
	base int64
	end  int64
	read int64
}

// Read implements io.Reader.
func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.r.Read(p)
	if n > 0 {
		pr.read += int64(n)
		value := pr.base + pr.read
		if value > pr.end {
			value = pr.end
		}
		pr.slot.store(value)
	}
	return n, err
}
