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
// Difference from the C++ class (intentional): DownloadInfo is used through a
// pointer returned by NewDownloadInfo; the C++ lock-protected value
// copy/assignment is replaced by explicit sharing, which is the idiomatic Go
// approach for a lock-protected state holder.
type DownloadInfo struct {
	mu       sync.Mutex
	filename string
	status   DLStatus
	progress ProgressInfo
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
