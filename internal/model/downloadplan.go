package model

// DownloadPlan is the complete, pure-data description of one transfer run. It
// is built once and then read-only: it never starts workers, opens connections
// or touches the filesystem.
//
// Tasks is what transfer.Run consumes. Deletes is what core removes before
// transfer runs (the old-build diff, review D21); core's own executor applies
// it, because deleting old versions is install policy, not transport. The two
// live in one struct because one plan builder produces both, but they never
// travel together — Run takes []FileTask, never this struct, so transfer cannot
// reach Deletes.
type DownloadPlan struct {
	Tasks   []FileTask
	Deletes []string
}
