package guide

import "sync"

// RepairQueue tracks committed writes whose cache fill failed. Share it across
// all readers/writers for the same source/cache. It is not a durable outbox.
type RepairQueue struct {
	mu       sync.Mutex
	versions map[string]int64
	limit    int
	overflow bool
}

func NewRepairQueue(limit int) *RepairQueue {
	if limit <= 0 {
		panic("repair limit must be positive")
	}
	return &RepairQueue{versions: map[string]int64{}, limit: limit}
}
func (q *RepairQueue) mark(id string, version int64) {
	if q == nil {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if _, ok := q.versions[id]; !ok && len(q.versions) >= q.limit {
		q.overflow = true
		return
	}
	if version > q.versions[id] {
		q.versions[id] = version
	}
}
func (q *RepairQueue) pending(id string) bool {
	if q == nil {
		return false
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	_, ok := q.versions[id]
	return ok || q.overflow
}
func (q *RepairQueue) clear(id string, version int64) {
	if q == nil {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if floor, ok := q.versions[id]; ok && version >= floor {
		delete(q.versions, id)
	}
	// Overflow conservatively disables unvalidated reads for this queue's lifetime.
}
