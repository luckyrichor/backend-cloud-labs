// Package coherence implements the experiment's L1 + Redis topology. Each
// instance owns its L1; the L2 and source are shared. No public listener is added.
package coherence

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/cache"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/catalog"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/guide"
)

type Cache struct {
	Local         *cache.Memory
	Shared        guide.Cache
	Delay         time.Duration
	DelayedErrors atomic.Uint64
	mu            sync.Mutex
	closing       bool
	wg            sync.WaitGroup
}

func (c *Cache) Get(ctx context.Context, id string) (catalog.Item, error) {
	item, err := c.Local.Get(ctx, id)
	if err == nil {
		return item, nil
	}
	item, err = c.Shared.Get(ctx, id)
	if err != nil {
		return item, err
	}
	_, err = c.Local.PutIfNewer(ctx, item)
	return item, err
}
func (c *Cache) PutIfNewer(ctx context.Context, item catalog.Item) (bool, error) {
	applied, err := c.Shared.PutIfNewer(ctx, item)
	if err != nil {
		return false, err
	}
	local, err := c.Local.PutIfNewer(ctx, item)
	return applied || local, err
}
func (c *Cache) Set(ctx context.Context, item catalog.Item) error {
	u, ok := c.Shared.(guide.UngatedCache)
	if !ok {
		return guide.ErrUngatedCacheRequired
	}
	if err := u.Set(ctx, item); err != nil {
		return err
	}
	return c.Local.Set(ctx, item)
}
func (c *Cache) deleteNow(ctx context.Context, id string) error {
	u, ok := c.Shared.(guide.UngatedCache)
	if !ok {
		return guide.ErrUngatedCacheRequired
	}
	return errors.Join(c.Local.Delete(ctx, id), u.Delete(ctx, id))
}
func (c *Cache) Delete(ctx context.Context, id string) error {
	err := c.deleteNow(ctx, id)
	if c.Delay > 0 {
		c.mu.Lock()
		if !c.closing {
			c.wg.Add(1)
			go func() {
				defer c.wg.Done()
				time.Sleep(c.Delay)
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				if c.deleteNow(ctx, id) != nil {
					c.DelayedErrors.Add(1)
				}
			}()
		}
		c.mu.Unlock()
	}
	return err
}
func (c *Cache) Close() { c.mu.Lock(); c.closing = true; c.mu.Unlock(); c.wg.Wait() }
