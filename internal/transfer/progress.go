package transfer

import (
	"io"
	"sync"
	"sync/atomic"
)

// Progress is the running state of one run, in two layers: the run-level queue
// snapshot (the tasks Run was handed and their summed compressed size, published
// once before the first dispatch) and the per-task slots (the bytes a task has
// downloaded and its total, dropped when the task ends). A reader learns what is
// still pending by subtracting what it has seen start from the queue snapshot,
// and what is active from the slots.
//
// The asymmetry is deliberate: Queue keeps answering after Run returns, while
// Bytes and Total answer false once a task's slot is gone. A nil *Progress is a
// usable no-op.
type Progress struct {
	mu    sync.Mutex
	slots map[string]*progressSlot
	queue progressQueue
}

// progressQueue is the run-level queue snapshot: how many tasks the run started
// with and how many compressed bytes they carry. published says whether a
// snapshot exists at all, which is what keeps "no snapshot" distinct from "an
// empty queue".
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
// body.
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
// handed and their summed compressed size. ok is false when no run has published
// a snapshot yet — never "the queue is empty", which is what an empty run
// publishes instead.
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
// seen start.
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
// the sampler sees a transfer in flight rather than only the chunk boundaries the
// events report. The value is absolute (the attempt's base plus everything read
// so far), which keeps a retry that resumes from the bytes already in memory
// monotone, and it is clamped to the chunk's logical end so a server that ignores
// the Range cannot push the sample past it.
//
// A decrease is deliberate: when a failed hash discards the chunk buffer, the
// next attempt starts from the chunk's offset again, and the window that consumes
// these samples treats any decrease as a restart.
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
		pr.slot.store(min(pr.base+pr.read, pr.end))
	}
	return n, err
}
