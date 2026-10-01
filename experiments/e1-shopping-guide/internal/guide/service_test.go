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

// Deliberately unsafe cache-aside fill deterministically reproduces delayed old reader overwrite.
func TestReproduceStaleFill(t *testing.T) {
	ctx := context.Background()
	store := catalog.NewMemoryStore()
	old, _ := store.Put(ctx, catalog.Item{ID: "sku", PriceCent: 100})
	read, _ := store.Get(ctx, "sku") // reader pauses after source read
	newer, _ := store.Put(ctx, catalog.Item{ID: "sku", PriceCent: 200})
	naive := map[string]catalog.Item{"sku": newer} // writer invalidates/fills
	naive["sku"] = read                            // delayed reader re-inserts old version
	if naive["sku"].Version != old.Version || naive["sku"].PriceCent == newer.PriceCent {
		t.Fatal("failed to reproduce")
	}
}
func TestMonotonicFillRejectsDelayedReader(t *testing.T) {
	ctx := context.Background()
	store := catalog.NewMemoryStore()
	c := cache.NewMemory(time.Minute)
	old, _ := store.Put(ctx, catalog.Item{ID: "sku", PriceCent: 100})
	newer, _ := store.Put(ctx, catalog.Item{ID: "sku", PriceCent: 200})
	_, _ = c.PutIfNewer(ctx, newer)
	ok, err := c.PutIfNewer(ctx, old)
	if err != nil || ok {
		t.Fatal("old fill accepted")
	}
	result, err := (guide.Service{Store: store, Cache: c}).Get(ctx, "sku")
	if err != nil || result.Item.Version != 2 || result.Source != "cache_validated" {
		t.Fatal(result, err)
	}
}

type broken struct{}

func (broken) Get(context.Context, string) (catalog.Item, error) {
	return catalog.Item{}, errors.New("offline")
}
func (broken) PutIfNewer(context.Context, catalog.Item) (bool, error) {
	return false, errors.New("offline")
}
func TestCacheOutageAndStaleCacheAfterRecovery(t *testing.T) {
	ctx := context.Background()
	store := catalog.NewMemoryStore()
	c := cache.NewMemory(time.Minute)
	stable := guide.Service{Store: store, Cache: c}
	_, _ = stable.Put(ctx, catalog.Item{ID: "sku", PriceCent: 100, Stock: 1})
	offline := guide.Service{Store: store, Cache: broken{}}
	write, err := offline.Put(ctx, catalog.Item{ID: "sku", PriceCent: 200, Stock: 1})
	if err != nil || !write.Degraded {
		t.Fatal(write, err)
	}
	read, err := offline.Get(ctx, "sku")
	if err != nil || read.Item.Version != 2 || !read.Degraded {
		t.Fatal(read, err)
	}
	// Cache still holds v1 from before outage; validation repairs it on recovery.
	recovered, err := stable.Get(ctx, "sku")
	if err != nil || recovered.Item.Version != 2 {
		t.Fatal(recovered, err)
	}
	results, err := stable.Recommend(ctx, []string{"sku", "missing"}, 150)
	if err != nil || len(results) != 0 {
		t.Fatal(results, err)
	}
}
