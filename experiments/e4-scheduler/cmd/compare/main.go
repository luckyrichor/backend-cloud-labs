package main

import (
	"fmt"
	s "github.com/luckyrichor/backend-cloud-labs/e4-scheduler"
)

func main() {
	nodes := []s.Node{{ID: "a", Capacity: s.Resource{CPU: 8, Memory: 8}}, {ID: "b", Capacity: s.Resource{CPU: 8, Memory: 8}}}
	jobs := []s.Job{{ID: "one", Need: s.Resource{CPU: 4, Memory: 4}, Priority: 1}, {ID: "two", Need: s.Resource{CPU: 4, Memory: 4}, Priority: 1}, {ID: "urgent", Need: s.Resource{CPU: 8, Memory: 8}, Priority: 10}}
	fmt.Println("strategy,resident,rejected,preempted,urgent_admitted,nodes_used")
	for _, strategy := range []s.Strategy{s.Spread, s.BinPack, s.Preempt} {
		r, err := s.Schedule(nodes, jobs, strategy)
		if err != nil {
			panic(err)
		}
		used := map[string]bool{}
		urgent := false
		for _, p := range r.Placements {
			used[p.NodeID] = true
			urgent = urgent || p.Job.ID == "urgent"
		}
		fmt.Printf("%s,%d,%d,%d,%t,%d\n", strategy, len(r.Placements), len(r.Rejected), len(r.Preempted), urgent, len(used))
	}
}
