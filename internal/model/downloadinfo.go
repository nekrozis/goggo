package model

import "sync"

// DLStatus is a download-job state.
type DLStatus uint32

// DLStatusNotStarted and the following constants are the download-job states.
// NotStarted is 0 while the others are bit flags.
const (
	DLStatusNotStarted DLStatus = 0
	DLStatusStarting   DLStatus = 1 << 0
	DLStatusRunning    DLStatus = 1 << 1
	DLStatusFinished   DLStatus = 1 << 2
)

// ProgressInfo is a transfer progress snapshot. DLNow and DLTotal are byte
// counts; Rate and RateAvg are bytes per second.
type ProgressInfo struct {
	DLNow   int64
	DLTotal int64
	Rate    float64
	RateAvg float64
}

// DownloadInfo is the shared state between a download worker and the progress
// renderer, so every accessor takes a mutex.
//
// Always use the pointer returned by NewDownloadInfo: the struct embeds a
// sync.Mutex and must not be copied once in use.
//
// The lock is an exclusive sync.Mutex rather than an RWMutex because polling
// runs at much the same rate as updating, so readers would not gain from
// sharing.
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
