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
	Pending             []Job               // Evicted jobs still waiting after retry; Preempted is history.
	Usage               map[string]Resource // Final incremental resource ledger.
}
type Strategy string

const (
	Spread         Strategy = "spread"
	BinPack        Strategy = "binpack"
	Preempt        Strategy = "preempt" // Compatibility: spread placement plus preemption.
	PreemptBinPack Strategy = "preempt-binpack"
)

func fits(used, need, capacity Resource) bool {
	return need.CPU <= capacity.CPU-used.CPU && need.Memory <= capacity.Memory-used.Memory
}
func add(a, b Resource) Resource { return Resource{a.CPU + b.CPU, a.Memory + b.Memory} }

// Schedule models arrivals in input order. Preemption considers strictly lower
// priority residents; a failed attempt never removes any existing placement.
func Schedule(nodes []Node, jobs []Job, strategy Strategy) (Result, error) {
	var result Result
	if strategy != Spread && strategy != BinPack && strategy != Preempt && strategy != PreemptBinPack {
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
	used := make([]Resource, len(nodes))
	pack := strategy == BinPack || strategy == PreemptBinPack
	canPreempt := strategy == Preempt || strategy == PreemptBinPack
	choose := func(job Job) int {
		chosen := -1
		score := 0.0
		for i, n := range nodes {
			u := used[i]
			if !fits(u, job.Need, n.Capacity) {
				continue
			}
			s := float64(u.CPU)/float64(n.Capacity.CPU) + float64(u.Memory)/float64(n.Capacity.Memory)
			if chosen < 0 || (!pack && s < score) || (pack && s > score) {
				chosen, score = i, s
			}
		}
		return chosen
	}
	place := func(job Job, chosen int) {
		used[chosen] = add(used[chosen], job.Need)
		result.Placements = append(result.Placements, Placement{job, nodes[chosen].ID})
	}
	for _, job := range jobs {
		chosen := choose(job)
		if chosen < 0 && canPreempt {
			var best []Placement
			for i, n := range nodes {
				u := used[i]
				victims := []Placement{}
				for _, p := range result.Placements {
					if p.NodeID == n.ID && p.Job.Priority < job.Priority {
						victims = append(victims, p)
					}
				}
				// Keep lower priority first, then prefer resources useful to this deficit.
				// Stable arrival order resolves ties; this greedy heuristic is not an optimum.
				deficit := Resource{job.Need.CPU - (n.Capacity.CPU - u.CPU), job.Need.Memory - (n.Capacity.Memory - u.Memory)}
				relief := func(v Placement) float64 {
					score := 0.0
					if deficit.CPU > 0 {
						score += min(float64(v.Job.Need.CPU)/float64(deficit.CPU), 1)
					}
					if deficit.Memory > 0 {
						score += min(float64(v.Job.Need.Memory)/float64(deficit.Memory), 1)
					}
					return score
				}
				sort.SliceStable(victims, func(a, b int) bool {
					if victims[a].Job.Priority != victims[b].Job.Priority {
						return victims[a].Job.Priority < victims[b].Job.Priority
					}
					return relief(victims[a]) > relief(victims[b])
				})
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
					result.Pending = append(result.Pending, v.Job)
					used[chosen].CPU -= v.Job.Need.CPU
					used[chosen].Memory -= v.Job.Need.Memory
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
			place(job, chosen)
		}
		// Retry waiting evictions after each arrival, without further eviction:
		// no recursive preemption/oscillation, priority then stable queue order.
		sort.SliceStable(result.Pending, func(i, j int) bool { return result.Pending[i].Priority > result.Pending[j].Priority })
		waiting := make([]Job, 0, len(result.Pending))
		for _, retry := range result.Pending {
			if target := choose(retry); target >= 0 {
				place(retry, target)
			} else {
				waiting = append(waiting, retry)
			}
		}
		result.Pending = waiting
	}
	result.Usage = make(map[string]Resource, len(nodes))
	for i, n := range nodes {
		result.Usage[n.ID] = used[i]
	}
	return result, nil
}
