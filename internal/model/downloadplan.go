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
//
// SFC carries the small-files container groups: the container downloads as a
// task, the items inside it do not — their content is extracted from the
// container after transfer (S20). They are GalaxyDepotItem VALUES, not
// FileTasks, because they have no Destination of their own.
type DownloadPlan struct {
	Tasks   []FileTask
	Deletes []string
	SFC     []SFCGroup
}

// SFCGroup is one small-files container and the depot items whose bytes it
// carries (downloader.cpp:4268-4285 pairs them by product id at extraction
// time).
type SFCGroup struct {
	Container GalaxyDepotItem
	Items     []GalaxyDepotItem
}
