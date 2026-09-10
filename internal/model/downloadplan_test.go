package model

import "testing"

// TestDownloadPlanConstruction locks the two-part plan: the tasks transfer runs
// and the deletions core applies first stay separate fields of one struct. The
// zero value is nil on both sides, so an empty plan is representable.
func TestDownloadPlanConstruction(t *testing.T) {
	plan := DownloadPlan{
		Tasks:   []FileTask{{Destination: "/a"}, {Destination: "/b"}},
		Deletes: []string{"/old/removed.bin"},
	}
	if len(plan.Tasks) != 2 || plan.Tasks[0].Destination != "/a" {
		t.Errorf("plan tasks = %+v", plan.Tasks)
	}
	if len(plan.Deletes) != 1 || plan.Deletes[0] != "/old/removed.bin" {
		t.Errorf("plan deletes = %+v", plan.Deletes)
	}

	var zero DownloadPlan
	if zero.Tasks != nil || zero.Deletes != nil {
		t.Errorf("zero plan = %+v, want nil fields", zero)
	}
}
