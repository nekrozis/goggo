// Package transfer executes a set of file tasks against a CDN.
//
// It owns the HOW of downloading — the worker fan-out, the per-chunk byte ranges,
// the retry and resume arithmetic — and nothing about WHICH files to fetch or
// WHERE their URLs come from: the work arrives as []model.FileTask and the URLs
// are resolved through URLProvider, which core implements. Run drives the Galaxy
// chunks, RunWebsite the website files; the package depends on model and httpx
// only, never on ui, config, webapi or galaxy.
//
// A task resumes at the first byte missing from disk and leaves a complete file
// alone, so re-running a transfer fetches only what is missing. Progress and
// messages leave as Event values through Observer; the front end renders them.
package transfer
