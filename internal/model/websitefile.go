package model

// WebsiteTask is one website (non-chunked) file download. It mirrors the parts
// of gameFile the website worker consumes (downloader.cpp:2972-3450); the
// command wiring will build it from gameFile once the gamedetails face lands
// (registered in the S18c gap table), resolving the game file's type into the
// two behaviour flags below at that point.
type WebsiteTask struct {
	Destination string // absolute local path (gameFile::getFilepath's product)
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
