package guide

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/catalog"
	"strconv"
	"sync"
	"sync/atomic"
)

// RepairQueue owns a private cache namespace for one source lifetime. All users
// of that source/cache must share this queue. It is not a durable outbox.
type RepairQueue struct {
	mu       sync.Mutex
	versions map[string]int64
	limit    int
	dirty    atomic.Bool
	epoch    atomic.Uint64
	scope    atomic.Pointer[cacheNamespace]
	prefix   string
	fast     bool // Immutable matched-benchmark switch; default true.
}

func NewRepairQueue(limit int) *RepairQueue { return NewRepairQueueWithFastPath(limit, true) }
func NewRepairQueueWithFastPath(limit int, fast bool) *RepairQueue {
	if limit <= 0 {
		panic("repair limit must be positive")
	}
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		panic(err)
	}
	q := &RepairQueue{versions: map[string]int64{}, limit: limit, fast: fast, prefix: "epoch:" + hex.EncodeToString(token[:]) + ":"}
	q.scope.Store(&cacheNamespace{0, q.prefix + "0:"})
	return q
}
func (q *RepairQueue) mark(id string, version int64) {
	if q == nil {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if _, ok := q.versions[id]; !ok && len(q.versions) >= q.limit {
		// New readers can no longer reach any old fill, including in-flight fills.
		epoch := q.epoch.Add(1)
		q.scope.Store(&cacheNamespace{epoch, q.prefix + strconv.FormatUint(epoch, 10) + ":"})
		q.versions = map[string]int64{}
		q.dirty.Store(false)
		return
	}
	if version > q.versions[id] {
		q.versions[id] = version
		q.dirty.Store(true)
	}
}
func (q *RepairQueue) pending(id string) bool {
	if q == nil || (q.fast && !q.dirty.Load()) {
		return false
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	_, ok := q.versions[id]
	return ok
}
func (q *RepairQueue) clear(id string, version int64) {
	if q != nil {
		q.clearFor(q.epoch.Load(), id, version)
	}
}
func (q *RepairQueue) clearFor(epoch uint64, id string, version int64) {
	if q == nil || (q.fast && !q.dirty.Load()) {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if epoch != q.epoch.Load() {
		return
	} // An old request cannot clear the new generation.
	if floor, ok := q.versions[id]; ok && version >= floor {
		delete(q.versions, id)
	}
	q.dirty.Store(len(q.versions) != 0)
}

// A write committed after rotation must invalidate the current generation even
// when filling its captured old namespace succeeded. Check under the queue lock.
func (q *RepairQueue) finishWrite(epoch uint64, id string, version int64, filled bool) {
	if q == nil {
		return
	}
	if !filled {
		q.mark(id, version)
		return
	}
	q.mu.Lock()
	if epoch != q.epoch.Load() {
		q.mu.Unlock()
		q.mark(id, version)
		return
	}
	if floor, ok := q.versions[id]; ok && version >= floor {
		delete(q.versions, id)
	}
	q.dirty.Store(len(q.versions) != 0)
	q.mu.Unlock()
}

// Cache view is captured before the source read/write; its namespace never
// changes. Old readers cannot fill a later generation. Physical cached IDs are
// namespaced too; this adapter restores public IDs on read.
type cacheNamespace struct {
	generation uint64
	prefix     string
}

type cacheView struct {
	Cache
	prefix string
}

func (c cacheView) Get(ctx context.Context, id string) (catalog.Item, error) {
	item, err := c.Cache.Get(ctx, c.prefix+id)
	if err == nil {
		item.ID = id
	}
	return item, err
}
func (c cacheView) PutIfNewer(ctx context.Context, item catalog.Item) (bool, error) {
	item.ID = c.prefix + item.ID
	return c.Cache.PutIfNewer(ctx, item)
}
func (s Service) snapshotCache() Service {
	if s.Repairs != nil {
		scope := s.Repairs.scope.Load()
		s.generation = scope.generation
		s.Cache = cacheView{s.Cache, scope.prefix}
	}
	return s
}
