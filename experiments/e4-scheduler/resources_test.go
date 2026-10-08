package scheduler

import "testing"

func TestBothResourcesConstrainPlacement(t *testing.T) {
	nodes := []Node{{"cpu", Resource{8, 2}}, {"memory", Resource{2, 8}}}
	jobs := []Job{{"neither", Resource{4, 4}, 1}, {"cpu-job", Resource{4, 1}, 1}, {"memory-job", Resource{1, 4}, 1}}
	for _, policy := range []Strategy{Spread, BinPack, Preempt, PreemptBinPack} {
		result, err := Schedule(nodes, jobs, policy)
		if err != nil || len(result.Rejected) != 1 || result.Rejected[0].ID != "neither" || len(result.Placements) != 2 {
			t.Fatalf("%s: %+v", policy, result)
		}
		for _, p := range result.Placements {
			if (p.Job.ID == "cpu-job" && p.NodeID != "cpu") || (p.Job.ID == "memory-job" && p.NodeID != "memory") {
				t.Fatalf("resource mismatch: %+v", p)
			}
		}
	}
}
