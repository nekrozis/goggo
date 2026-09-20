package model

// FileTask is one file a transfer run downloads.
//
// Item is the depot entry the plan builder resolved and Destination is the absolute
// local path it worked out: a transfer run never re-derives paths, it reads
// Item.Path for the relative name and writes to Destination.
//
// Item is copied by value, which shallow-copies its Chunks slice: the plan and the
// task share one backing array, which is fine for a read-only plan.
type FileTask struct {
	Item        GalaxyDepotItem
	Destination string
}
