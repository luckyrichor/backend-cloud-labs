package guide

import (
	"sync"
	"sync/atomic"
)

// RepairQueue tracks committed writes whose cache fill failed. Share it across
// all readers/writers for the same source/cache. It is not a durable outbox.
type RepairQueue struct {
	mu       sync.Mutex
	versions map[string]int64
	limit    int
	overflow bool
	dirty    atomic.Bool // True when any mark or sticky overflow exists; published under mu.
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
		q.dirty.Store(true)
		return
	}
	if version > q.versions[id] {
		q.versions[id] = version
		q.dirty.Store(true)
	}
}
func (q *RepairQueue) pending(id string) bool {
	if q == nil || !q.dirty.Load() {
		return false
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	_, ok := q.versions[id]
	return ok || q.overflow
}
func (q *RepairQueue) clear(id string, version int64) {
	if q == nil || !q.dirty.Load() {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if floor, ok := q.versions[id]; ok && version >= floor {
		delete(q.versions, id)
	}
	q.dirty.Store(q.overflow || len(q.versions) != 0)
	// Overflow conservatively disables unvalidated reads for this queue's lifetime.
}
