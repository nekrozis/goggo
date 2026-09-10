package model

import "sync"

// DLStatus is a download-job state (include/downloadinfo.h:13-16).
type DLStatus uint32

// Download status values. Note that NotStarted is 0 while the other states
// use bit flags, mirroring the original constants.
const (
	DLStatusNotStarted DLStatus = 0
	DLStatusStarting   DLStatus = 1 << 0
	DLStatusRunning    DLStatus = 1 << 1
	DLStatusFinished   DLStatus = 1 << 2
)

// ProgressInfo mirrors struct progressInfo (downloadinfo.h:18-24). DLNow and
// DLTotal are byte counts.
type ProgressInfo struct {
	DLNow   int64
	DLTotal int64
	Rate    float64
	RateAvg float64
}

// DownloadInfo mirrors class DownloadInfo (downloadinfo.h:26-93). It is the
// shared state between a download worker and the progress renderer, so reads
// and writes are guarded by a mutex.
//
// Difference from the C++ class (intentional): the C++ lock-protected value
// copy/assignment is replaced by explicit sharing, so a DownloadInfo is always
// used through the pointer returned by NewDownloadInfo. Do not copy it by value
// once in use: it embeds a sync.Mutex, and Go's sync types must not be copied.
// That is a Go implementation constraint, not a claim that the C++ class was
// uncopyable — the original does define a lock-guarded copy constructor and
// assignment operator.
//
// The lock deliberately stays an exclusive sync.Mutex: the C++ class guards
// every accessor, getters included, with std::mutex, and progress polling runs
// at much the same rate as progress updates, so an RWMutex would add cost
// without a clear benefit. Readers taking the exclusive lock mirrors the
// original behaviour.
//
// Fields are ordered to minimise padding: progress (32B), filename (16B), the
// mutex (8B) and the 4B status. An RWMutex (24B) would not change the total
// (80B in either order), another reason the layout is left alone; these sizes
// are the current amd64 toolchain baseline, not an ABI guarantee.
type DownloadInfo struct {
	progress ProgressInfo
	filename string
	mu       sync.Mutex
	status   DLStatus
}

// NewDownloadInfo returns a DownloadInfo in the NotStarted state.
func NewDownloadInfo() *DownloadInfo {
	return &DownloadInfo{}
}

// SetFilename stores the file name under download.
func (d *DownloadInfo) SetFilename(name string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.filename = name
}

// GetFilename returns the file name under download.
func (d *DownloadInfo) GetFilename() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.filename
}

// SetStatus updates the download state.
func (d *DownloadInfo) SetStatus(s DLStatus) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.status = s
}

// GetStatus returns the current download state.
func (d *DownloadInfo) GetStatus() DLStatus {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.status
}

// SetProgressInfo replaces the transfer progress snapshot.
func (d *DownloadInfo) SetProgressInfo(p ProgressInfo) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.progress = p
}

// GetProgressInfo returns a copy of the transfer progress snapshot.
func (d *DownloadInfo) GetProgressInfo() ProgressInfo {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.progress
}
