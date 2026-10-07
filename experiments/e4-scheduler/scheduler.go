package scheduler

import (
	"fmt"
	"sort"
)

type Resource struct{ CPU, Memory int }
type Job struct {
	ID       string
	Need     Resource
	Priority int
}
type Node struct {
	ID       string
	Capacity Resource
}
type Placement struct {
	Job    Job
	NodeID string
}
type Result struct {
	Placements          []Placement
	Rejected, Preempted []Job
}
type Strategy string

const (
	Spread  Strategy = "spread"
	BinPack Strategy = "binpack"
	Preempt Strategy = "preempt"
)

func fits(used, need, capacity Resource) bool {
	return need.CPU <= capacity.CPU-used.CPU && need.Memory <= capacity.Memory-used.Memory
}
func add(a, b Resource) Resource { return Resource{a.CPU + b.CPU, a.Memory + b.Memory} }
func usage(node Node, placements []Placement) Resource {
	var used Resource
	for _, p := range placements {
		if p.NodeID == node.ID {
			used = add(used, p.Job.Need)
		}
	}
	return used
}

// Schedule models arrivals in input order. Preemption considers strictly lower
// priority residents; a failed attempt never removes any existing placement.
func Schedule(nodes []Node, jobs []Job, strategy Strategy) (Result, error) {
	var result Result
	if strategy != Spread && strategy != BinPack && strategy != Preempt {
		return result, fmt.Errorf("unknown strategy")
	}
	ids := map[string]bool{}
	for _, n := range nodes {
		if n.ID == "" || ids[n.ID] || n.Capacity.CPU <= 0 || n.Capacity.Memory <= 0 {
			return result, fmt.Errorf("invalid node")
		}
		ids[n.ID] = true
	}
	ids = map[string]bool{}
	for _, j := range jobs {
		if j.ID == "" || ids[j.ID] || j.Need.CPU <= 0 || j.Need.Memory <= 0 {
			return result, fmt.Errorf("invalid job")
		}
		ids[j.ID] = true
	}
	for _, job := range jobs {
		chosen := -1
		score := 0.0
		for i, n := range nodes {
			u := usage(n, result.Placements)
			if !fits(u, job.Need, n.Capacity) {
				continue
			}
			s := float64(u.CPU)/float64(n.Capacity.CPU) + float64(u.Memory)/float64(n.Capacity.Memory)
			if chosen < 0 || (strategy != BinPack && s < score) || (strategy == BinPack && s > score) {
				chosen = i
				score = s
			}
		}
		if chosen < 0 && strategy == Preempt {
			var best []Placement
			for i, n := range nodes {
				u := usage(n, result.Placements)
				victims := []Placement{}
				for _, p := range result.Placements {
					if p.NodeID == n.ID && p.Job.Priority < job.Priority {
						victims = append(victims, p)
					}
				}
				sort.SliceStable(victims, func(a, b int) bool { return victims[a].Job.Priority < victims[b].Job.Priority })
				removed := []Placement{}
				for _, v := range victims {
					u.CPU -= v.Job.Need.CPU
					u.Memory -= v.Job.Need.Memory
					removed = append(removed, v)
					if fits(u, job.Need, n.Capacity) {
						break
					}
				}
				if fits(u, job.Need, n.Capacity) && (chosen < 0 || len(removed) < len(best)) {
					chosen = i
					best = removed
				}
			}
			if chosen >= 0 {
				victimIDs := map[string]bool{}
				for _, v := range best {
					victimIDs[v.Job.ID] = true
					result.Preempted = append(result.Preempted, v.Job)
				}
				kept := []Placement{}
				for _, p := range result.Placements {
					if !victimIDs[p.Job.ID] {
						kept = append(kept, p)
					}
				}
				result.Placements = kept
			}
		}
		if chosen < 0 {
			result.Rejected = append(result.Rejected, job)
		} else {
			result.Placements = append(result.Placements, Placement{job, nodes[chosen].ID})
		}
	}
	return result, nil
}
