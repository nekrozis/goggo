package model

// WebsiteTask is one website (non-chunked) file download. It carries the parts
// of a game file the website worker needs; the two behaviour flags below are
// resolved from the game file's type by whoever builds the task.
type WebsiteTask struct {
	Destination string // absolute local path
	DownlinkURL string // the galaxy API's downlink JSON url
	Gamename    string
	Size        string // the API-reported size string (unparsable forms count as 0)
	// Checksummed marks installers and patches: the downlink document may
	// carry a checksum url whose XML md5 feeds the version check.
	Checksummed bool
	// Extra marks extras: the version/complete check is the size matrix
	// against the API size, the local XML or a content-length probe.
	Extra bool
}
