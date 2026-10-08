package scheduler

import (
	"fmt"
	"math/rand"
	"testing"
)

func TestEvictedJobMovesToSpareNode(t *testing.T) {
	nodes := []Node{{"a", Resource{8, 8}}, {"b", Resource{8, 8}}}
	jobs := []Job{{"one", Resource{4, 4}, 1}, {"two", Resource{4, 4}, 1}, {"urgent", Resource{8, 8}, 10}}
	r, err := Schedule(nodes, jobs, Preempt)
	if err != nil || len(r.Placements) != 3 || len(r.Preempted) != 1 || len(r.Pending) != 0 {
		t.Fatalf("%+v %v", r, err)
	}
	for _, p := range r.Placements {
		if p.Job.ID == "one" && p.NodeID != "b" {
			t.Fatalf("victim not migrated: %+v", p)
		}
	}
}

func TestEvictionsStayPendingWithoutOscillation(t *testing.T) {
	nodes := []Node{{"a", Resource{8, 8}}}
	jobs := []Job{{"low", Resource{8, 8}, 1}, {"mid", Resource{8, 8}, 2}, {"high", Resource{8, 8}, 3}}
	for _, policy := range []Strategy{Preempt, PreemptBinPack} {
		r, err := Schedule(nodes, jobs, policy)
		if err != nil || len(r.Placements) != 1 || r.Placements[0].Job.ID != "high" || len(r.Pending) != 2 || len(r.Preempted) != 2 || len(r.Rejected) != 0 {
			t.Fatalf("%+v %v", r, err)
		}
		if r.Pending[0].ID != "mid" || r.Pending[1].ID != "low" {
			t.Fatal("queue priority order")
		}
	}
}

func TestLedgerAndJobConservationAfterEveryArrival(t *testing.T) {
	random := rand.New(rand.NewSource(19))
	nodes := []Node{{"a", Resource{24, 32}}, {"b", Resource{16, 24}}, {"c", Resource{8, 16}}}
	jobs := make([]Job, 80)
	for i := range jobs {
		jobs[i] = Job{fmt.Sprint(i), Resource{1 + random.Intn(16), 1 + random.Intn(24)}, random.Intn(10)}
	}
	for _, policy := range []Strategy{Spread, BinPack, Preempt, PreemptBinPack} {
		for length := 0; length <= len(jobs); length++ {
			r, err := Schedule(nodes, jobs[:length], policy)
			if err != nil {
				t.Fatal(err)
			}
			seen := map[string]bool{}
			check := func(j Job) {
				if seen[j.ID] {
					t.Fatalf("duplicate job %s", j.ID)
				}
				seen[j.ID] = true
			}
			for _, p := range r.Placements {
				check(p.Job)
			}
			for _, j := range r.Pending {
				check(j)
			}
			for _, j := range r.Rejected {
				check(j)
			}
			if len(seen) != length {
				t.Fatalf("lost job: %s %d %+v", policy, length, r)
			}
			for _, n := range nodes {
				u := usage(n, r.Placements)
				if u != r.Usage[n.ID] || !fits(Resource{}, u, n.Capacity) {
					t.Fatalf("ledger drift: %+v", r)
				}
			}
		}
	}
}

func BenchmarkIncrementalPlacement(b *testing.B) {
	for _, size := range []int{1000, 10000} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			nodes := make([]Node, 32)
			for i := range nodes {
				nodes[i] = Node{fmt.Sprint(i), Resource{size, size}}
			}
			jobs := make([]Job, size)
			for i := range jobs {
				jobs[i] = Job{fmt.Sprint(i), Resource{1, 2}, 1}
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				r, err := Schedule(nodes, jobs, BinPack)
				if err != nil || len(r.Placements) != size {
					b.Fatal("placement failed")
				}
			}
		})
	}
}

// Independent recomputation oracle; production placement never calls it.
func usage(node Node, placements []Placement) Resource {
	var used Resource
	for _, p := range placements {
		if p.NodeID == node.ID {
			used = add(used, p.Job.Need)
		}
	}
	return used
}
