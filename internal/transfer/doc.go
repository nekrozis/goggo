// Package transfer executes a set of file tasks against a CDN.
//
// It owns the HOW of downloading — the worker fan-out, the per-chunk byte
// ranges, the retry and resume arithmetic — and nothing about WHICH files to
// fetch or WHERE their URLs come from: the work arrives as []model.FileTask and
// the URLs are resolved through URLProvider, which core implements.
//
// Dependencies are one-directional: transfer may depend on model and httpx, and
// it never imports ui, config, webapi or galaxy. Anything the front end needs
// to see (progress, messages) leaves as Event values through Observer, and the
// front end decides how to render them.
//
// This package currently declares its contracts — the task, plan and option
// shapes, the two seams and the dependency bundle. The run loop that consumes
// them arrives in the next step.
package transfer
