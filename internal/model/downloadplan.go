package model

// DownloadPlan is the complete, pure-data description of one transfer run: built
// once, then read-only, with no workers, connections or filesystem access.
//
// Tasks is what transfer.Run consumes; Deletes is what core removes before transfer
// runs, applied by core's own executor because deleting old versions is install
// policy, allowed by default, not transport. One plan builder produces both, but
// they never travel together — Run takes []FileTask, never this struct, so transfer
// cannot reach Deletes.
//
// SFC carries the small-files container groups: the container downloads as a task,
// the items inside it do not, because their bytes come out of the container
// afterwards and they have no Destination of their own.
type DownloadPlan struct {
	Tasks   []FileTask
	Deletes []string
	SFC     []SFCGroup
}

// SFCGroup is one small-files container and the depot items whose bytes it
// carries.
type SFCGroup struct {
	Container GalaxyDepotItem
	Items     []GalaxyDepotItem
}
