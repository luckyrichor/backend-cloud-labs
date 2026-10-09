package cache

import (
	"context"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/catalog"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/guide"
	"sync"
	"time"
)

type entry struct {
	item    catalog.Item
	expires time.Time
}
type Memory struct {
	mu            sync.RWMutex
	entries       map[string]entry
	TTL           time.Duration
	now           func() time.Time
	parallelReads bool
	writes        uint64
}

func NewMemory(ttl time.Duration) *Memory { return NewMemoryWithClock(ttl, time.Now) }

// Clock is immutable after construction; tests advance a controlled clock.
func NewMemoryWithClock(ttl time.Duration, now func() time.Time) *Memory {
	if now == nil {
		panic("clock is required")
	}
	return &Memory{entries: map[string]entry{}, TTL: ttl, now: now, parallelReads: true}
}
func (c *Memory) Get(ctx context.Context, id string) (catalog.Item, error) {
	if err := ctx.Err(); err != nil {
		return catalog.Item{}, err
	}
	if c.parallelReads {
		c.mu.RLock()
		defer c.mu.RUnlock()
	} else {
		c.mu.Lock()
		defer c.mu.Unlock()
	}
	e, ok := c.entries[id]
	if !ok || c.now().After(e.expires) {
		return catalog.Item{}, guide.ErrCacheMiss
	}
	return e.item, nil
}
func (c *Memory) PutIfNewer(ctx context.Context, item catalog.Item) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[item.ID]
	if ok && c.now().Before(e.expires) && e.item.Version > item.Version {
		return false, nil
	}
	now := c.now()
	c.writes++
	if c.writes%256 == 0 {
		for id, entry := range c.entries {
			if !now.Before(entry.expires) {
				delete(c.entries, id)
			}
		}
	}
	c.entries[item.ID] = entry{item, now.Add(c.TTL)}
	return true, nil
}

// NewMemoryWithReadLock controls the immutable read-lock mode for matched
// benchmarks. Normal constructors use concurrent read locks.
func NewMemoryWithReadLock(ttl time.Duration, parallel bool) *Memory {
	c := NewMemory(ttl)
	c.parallelReads = parallel
	return c
}
