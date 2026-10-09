package main

import (
	"fmt"
	s "github.com/luckyrichor/backend-cloud-labs/experiments/e4-scheduler"
)

func main() {
	scenarios := []struct {
		name string
		jobs []s.Job
	}{
		{"packing", []s.Job{job("one", 4, 1), job("two", 4, 1), job("urgent", 8, 10)}},
		{"full_nodes", []s.Job{job("low-a", 8, 1), job("low-b", 8, 1), job("urgent", 8, 10)}},
		{"packing_and_preemption", []s.Job{job("small-a", 2, 1), job("large-a", 6, 1), job("small-b", 2, 1), job("large-b", 6, 1), job("urgent", 8, 10)}},
	}
	nodes := []s.Node{{ID: "a", Capacity: s.Resource{CPU: 8, Memory: 8}}, {ID: "b", Capacity: s.Resource{CPU: 8, Memory: 8}}}
	fmt.Println("scenario,strategy,resident,rejected,preempted,pending,urgent_admitted,nodes_used,cpu_used,interrupted_cpu,interrupted_memory")
	for _, scenario := range scenarios {
		for _, strategy := range []s.Strategy{s.Spread, s.BinPack, s.Preempt, s.PreemptBinPack} {
			r, err := s.Schedule(nodes, scenario.jobs, strategy)
			if err != nil {
				panic(err)
			}
			used := map[string]bool{}
			urgent := false
			cpu := 0
			for _, p := range r.Placements {
				used[p.NodeID] = true
				urgent = urgent || p.Job.ID == "urgent"
				cpu += p.Job.Need.CPU
			}
			interrupted := r.InterruptedResources()
			fmt.Printf("%s,%s,%d,%d,%d,%d,%t,%d,%d,%d,%d\n", scenario.name, strategy, len(r.Placements), len(r.Rejected), len(r.Preempted), len(r.Pending), urgent, len(used), cpu, interrupted.CPU, interrupted.Memory)
		}
	}
}

func job(id string, size, priority int) s.Job {
	return s.Job{ID: id, Need: s.Resource{CPU: size, Memory: size}, Priority: priority}
}
