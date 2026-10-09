package guide_test

import (
	"context"
	"errors"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/cache"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/catalog"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/guide"
	"testing"
	"time"
)

type switchCache struct {
	guide.Cache
	offline bool
}

func (c *switchCache) PutIfNewer(ctx context.Context, item catalog.Item) (bool, error) {
	if c.offline {
		return false, errors.New("offline")
	}
	return c.Cache.PutIfNewer(ctx, item)
}
func TestFailedWriteRepairBypassesStaleHitBeforeTTL(t *testing.T) {
	ctx := context.Background()
	store := catalog.NewMemoryStore()
	c := &switchCache{Cache: cache.NewMemory(time.Hour)}
	s := guide.Service{Store: store, Cache: c, Mode: guide.CacheFirst, Repairs: guide.NewRepairQueue(4)}
	_, _ = s.Put(ctx, catalog.Item{ID: "sku", PriceCent: 100})
	c.offline = true
	r, err := s.Put(ctx, catalog.Item{ID: "sku", PriceCent: 200})
	if err != nil || !r.Degraded {
		t.Fatal(r, err)
	}
	r, err = s.Get(ctx, "sku")
	if err != nil || r.Item.Version != 2 || !r.Degraded {
		t.Fatal("stale while repair failed", r, err)
	}
	c.offline = false
	r, err = s.Get(ctx, "sku")
	if err != nil || r.Item.Version != 2 || r.Source != "store" {
		t.Fatal("did not repair", r, err)
	}
	r, err = s.Get(ctx, "sku")
	if err != nil || r.Item.Version != 2 || r.Source != "cache_unvalidated" {
		t.Fatal("did not resume hits", r, err)
	}
}
