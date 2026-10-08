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
