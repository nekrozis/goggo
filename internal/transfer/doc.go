// Package transfer executes a set of file tasks against a CDN.
//
// It owns the HOW of downloading — the worker fan-out, the per-chunk byte ranges,
// the retry and resume arithmetic — and nothing about WHICH files to fetch or
// WHERE their URLs come from: the work arrives as []model.FileTask and the URLs
// are resolved through URLProvider, which core implements. Run drives the Galaxy
// chunks, RunWebsite the website files; progress and messages leave as Event
// values through Observer.
package transfer
