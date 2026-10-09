package guide

import "testing"

func TestRepairVersionFenceAndBoundedOverflow(t *testing.T) {
	q := NewRepairQueue(1)
	q.mark("a", 2)
	q.clear("a", 1)
	if !q.pending("a") {
		t.Fatal("old reader cleared newer repair")
	}
	q.mark("b", 3)
	q.clear("a", 2)
	if len(q.versions) > 1 || !q.pending("any-key") {
		t.Fatal("overflow dropped safety")
	}
}
