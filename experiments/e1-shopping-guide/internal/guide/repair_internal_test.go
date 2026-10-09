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

func TestRepairFastPathTransitions(t *testing.T) {
	q := NewRepairQueue(2)
	if q.dirty.Load() || q.pending("a") {
		t.Fatal("new queue dirty")
	}
	q.mark("a", 2)
	q.mark("b", 3)
	q.clear("a", 2)
	if !q.dirty.Load() || !q.pending("b") {
		t.Fatal("lost other mark")
	}
	q.clear("b", 2)
	if !q.dirty.Load() {
		t.Fatal("old reader cleared state")
	}
	q.clear("b", 3)
	if q.dirty.Load() || q.pending("a") {
		t.Fatal("empty queue stayed dirty")
	}
}

// Isolates the queue from store/cache locks. locked is the preceding implementation's
// empty pending read; both cases share one queue across RunParallel workers.
func BenchmarkRepairReadParallel(b *testing.B) {
	for _, name := range []string{"locked-empty", "atomic-empty", "atomic-marked"} {
		b.Run(name, func(b *testing.B) {
			q := NewRepairQueue(1024)
			if name == "atomic-marked" {
				q.mark("other", 2)
			}
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					if name == "locked-empty" {
						q.mu.Lock()
						_, ok := q.versions["sku"]
						pending := ok || q.overflow
						q.mu.Unlock()
						if pending {
							b.Error("unexpected pending")
						}
					} else if q.pending("sku") {
						b.Error("unexpected pending")
					}
				}
			})
		})
	}
}
