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
	mu      sync.Mutex
	entries map[string]entry
	TTL     time.Duration
}

func NewMemory(ttl time.Duration) *Memory { return &Memory{entries: map[string]entry{}, TTL: ttl} }
func (c *Memory) Get(ctx context.Context, id string) (catalog.Item, error) {
	if err := ctx.Err(); err != nil {
		return catalog.Item{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[id]
	if !ok || time.Now().After(e.expires) {
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
	if ok && time.Now().Before(e.expires) && e.item.Version > item.Version {
		return false, nil
	}
	c.entries[item.ID] = entry{item, time.Now().Add(c.TTL)}
	return true, nil
}
