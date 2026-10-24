package guide_test

import (
	"context"
	"errors"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/cache"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/catalog"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/catalogtest"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/guide"
	"testing"
	"time"
)

// Deliberately unsafe cache-aside fill deterministically reproduces delayed old reader overwrite.
func TestReproduceStaleFill(t *testing.T) {
	ctx := context.Background()
	store := catalogtest.NewStore(t)
	old, _ := store.Put(ctx, catalog.Item{ID: "101", PriceCent: 100})
	read, _ := store.Get(ctx, "101") // reader pauses after source read
	newer, _ := store.Put(ctx, catalog.Item{ID: "101", PriceCent: 200})
	naive := map[string]catalog.Item{"101": newer} // writer invalidates/fills
	naive["101"] = read                            // delayed reader re-inserts old version
	if naive["101"].Version != old.Version || naive["101"].PriceCent == newer.PriceCent {
		t.Fatal("failed to reproduce")
	}
}
func TestMonotonicFillRejectsDelayedReader(t *testing.T) {
	ctx := context.Background()
	store := catalogtest.NewStore(t)
	c := cache.NewMemory(time.Minute)
	old, _ := store.Put(ctx, catalog.Item{ID: "101", PriceCent: 100})
	newer, _ := store.Put(ctx, catalog.Item{ID: "101", PriceCent: 200})
	_, _ = c.PutIfNewer(ctx, newer)
	ok, err := c.PutIfNewer(ctx, old)
	if err != nil || ok {
		t.Fatal("old fill accepted")
	}
	result, err := (guide.Service{Store: store, Cache: c}).Get(ctx, "101")
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
	store := catalogtest.NewStore(t)
	c := cache.NewMemory(time.Minute)
	stable := guide.Service{Store: store, Cache: c}
	_, _ = stable.Put(ctx, catalog.Item{ID: "101", PriceCent: 100, Stock: 1})
	offline := guide.Service{Store: store, Cache: broken{}}
	write, err := offline.Put(ctx, catalog.Item{ID: "101", PriceCent: 200, Stock: 1})
	if err != nil || !write.Degraded {
		t.Fatal(write, err)
	}
	read, err := offline.Get(ctx, "101")
	if err != nil || read.Item.Version != 2 || !read.Degraded {
		t.Fatal(read, err)
	}
	// Cache still holds v1 from before outage; validation repairs it on recovery.
	recovered, err := stable.Get(ctx, "101")
	if err != nil || recovered.Item.Version != 2 {
		t.Fatal(recovered, err)
	}
	results, err := stable.Recommend(ctx, []string{"101", "998"}, 150)
	if err != nil || len(results) != 0 {
		t.Fatal(results, err)
	}
}

func TestRecommendationsDeduplicateAndSortDeterministically(t *testing.T) {
	ctx := context.Background()
	service := guide.Service{Store: catalogtest.NewStore(t), Cache: cache.NewMemory(time.Minute)}
	for _, item := range []catalog.Item{
		{ID: "2", PriceCent: 100, Stock: 1}, {ID: "1", PriceCent: 100, Stock: 1},
		{ID: "3", PriceCent: 50, Stock: 1}, {ID: "4", PriceCent: 1, Stock: 0},
	} {
		if _, err := service.Put(ctx, item); err != nil {
			t.Fatal(err)
		}
	}
	result, err := service.Recommend(ctx, []string{"2", "1", "2", "3", "998", "4"}, 100)
	if err != nil || len(result) != 3 || result[0].Item.ID != "3" ||
		result[1].Item.ID != "1" || result[2].Item.ID != "2" {
		t.Fatal(result, err)
	}
}
