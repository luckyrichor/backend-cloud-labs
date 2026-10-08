package scheduler

import "testing"

func TestPolicies(t *testing.T) {
	nodes := []Node{{"a", Resource{8, 8}}, {"b", Resource{8, 8}}}
	jobs := []Job{{"one", Resource{4, 4}, 1}, {"two", Resource{4, 4}, 1}, {"urgent", Resource{8, 8}, 10}}
	expected := map[Strategy][3]int{Spread: {2, 1, 0}, BinPack: {3, 0, 0}, Preempt: {3, 0, 1}, PreemptBinPack: {3, 0, 0}}
	for s, want := range expected {
		r, err := Schedule(nodes, jobs, s)
		if err != nil || len(r.Placements) != want[0] || len(r.Rejected) != want[1] || len(r.Preempted) != want[2] {
			t.Fatalf("%s: %+v %v", s, r, err)
		}
		for _, n := range nodes {
			u := usage(n, r.Placements)
			if !fits(Resource{}, u, n.Capacity) {
				t.Fatal("overcommitted")
			}
		}
	}
}
func TestPreemptionAtomicAndPriority(t *testing.T) {
	nodes := []Node{{"a", Resource{8, 8}}}
	jobs := []Job{{"resident", Resource{8, 8}, 5}, {"equal", Resource{1, 1}, 5}, {"oversize", Resource{9, 1}, 10}}
	r, err := Schedule(nodes, jobs, Preempt)
	if err != nil || len(r.Placements) != 1 || len(r.Preempted) != 0 || len(r.Rejected) != 2 {
		t.Fatalf("%+v", r)
	}
}
func TestInvalidInputs(t *testing.T) {
	for _, jobs := range [][]Job{{{"a", Resource{-1, 1}, 1}}, {{"a", Resource{1, 1}, 1}, {"a", Resource{1, 1}, 2}}} {
		if _, err := Schedule([]Node{{"n", Resource{2, 2}}}, jobs, Spread); err == nil {
			t.Fatal("accepted invalid job")
		}
	}
	if _, err := Schedule([]Node{{"a", Resource{0, 1}}}, nil, Spread); err == nil {
		t.Fatal("accepted invalid node")
	}
	if _, err := Schedule(nil, nil, "unknown"); err == nil {
		t.Fatal("accepted strategy")
	}
}
