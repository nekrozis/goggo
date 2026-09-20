package model

// FileTask is one file a transfer run downloads.
//
// Item is the depot entry the plan builder resolved. It lives in model rather
// than in galaxy, so the transfer layer can depend on model without depending
// on galaxy; Destination is the absolute local path the plan builder worked it
// out to. A transfer run never re-derives paths: it reads Item.Path for the
// relative name and writes to Destination.
//
// Item is copied by value, which shallow-copies its Chunks slice: the plan and
// this task share one backing array. That is fine for a plan that is built once
// and then treated as read-only.
type FileTask struct {
	Item        GalaxyDepotItem
	Destination string
}
