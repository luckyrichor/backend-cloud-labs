package scheduler

import "testing"

func TestEqualPriorityChoosesUsefulLargeVictim(t *testing.T) {
	for _, strategy := range []Strategy{Preempt, PreemptBinPack} {
		r, err := Schedule([]Node{{"n", Resource{10, 10}}}, []Job{
			{"small", Resource{2, 2}, 1}, {"large", Resource{8, 8}, 1}, {"urgent", Resource{8, 8}, 10}}, strategy)
		if err != nil || len(r.Preempted) != 1 || r.Preempted[0].ID != "large" {
			t.Fatalf("%+v %v", r, err)
		}
	}
}

func TestCombinedStrategyAdmitsUrgentWithHigherUsageThanSpreadPreemption(t *testing.T) {
	nodes := []Node{{"a", Resource{8, 8}}, {"b", Resource{8, 8}}}
	jobs := []Job{{"small-a", Resource{2, 2}, 1}, {"large-a", Resource{6, 6}, 1}, {"small-b", Resource{2, 2}, 1}, {"large-b", Resource{6, 6}, 1}, {"urgent", Resource{8, 8}, 10}}
	for _, policy := range []Strategy{BinPack, Preempt, PreemptBinPack} {
		r, err := Schedule(nodes, jobs, policy)
		if err != nil {
			t.Fatal(err)
		}
		urgent := false
		used := 0
		for _, p := range r.Placements {
			urgent = urgent || p.Job.ID == "urgent"
			used += p.Job.Need.CPU
		}
		if policy == BinPack && urgent {
			t.Fatal("binpack should reject urgent")
		}
		if policy == Preempt && (!urgent || used != 12) {
			t.Fatal(r)
		}
		if policy == PreemptBinPack && (!urgent || used != 16 || len(r.Preempted) != 2) {
			t.Fatal(r)
		}
	}
}
