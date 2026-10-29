package guide_test

import (
	"context"
	"errors"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/cache"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/catalog"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/guide"
	"sync/atomic"
	"testing"
	"time"
)

type narrowStore struct {
	catalog.Store
	full, narrow atomic.Int64
}

func (s *narrowStore) Get(ctx context.Context, id string) (catalog.Item, error) {
	s.full.Add(1)
	return s.Store.Get(ctx, id)
}
func (s *narrowStore) GetVersion(ctx context.Context, id string) (int64, error) {
	s.narrow.Add(1)
	v, err := s.Store.Get(ctx, id)
	return v.Version, err
}
func TestStrictVersionNoDoubleQueryOnMissAndRechecksHit(t *testing.T) {
	ctx := context.Background()
	base := catalog.NewMemoryStore()
	base.Put(ctx, catalog.Item{ID: "1", Title: "one"})
	store := &narrowStore{Store: base}
	s := guide.NewService(store, cache.NewMemory(time.Hour), guide.StrictVersion)
	s.Get(ctx, "1")
	if store.full.Load() != 1 || store.narrow.Load() != 0 {
		t.Fatal("miss must issue only full-row query")
	}
	s.Get(ctx, "1")
	if store.full.Load() != 1 || store.narrow.Load() != 1 {
		t.Fatal("matching hit must issue narrow query only")
	}
	base.Put(ctx, catalog.Item{ID: "1", Title: "two"})
	r, err := s.Get(ctx, "1")
	if err != nil || r.Item.Version != 2 || store.full.Load() != 2 || store.narrow.Load() != 2 {
		t.Fatalf("mismatch not refreshed %+v %v", r, err)
	}
}

type cancelAfterRead struct {
	catalog.Store
	cancel context.CancelFunc
	called bool
}

func (s *cancelAfterRead) Get(ctx context.Context, id string) (catalog.Item, error) {
	item, err := s.Store.Get(ctx, id)
	s.called = true
	s.cancel()
	return item, err
}
func TestFaultFillDelayHonorsCancellation(t *testing.T) {
	ctx := context.Background()
	store := catalog.NewMemoryStore()
	store.Put(ctx, catalog.Item{ID: "1"})
	cancelled, cancel := context.WithCancel(ctx)
	defer cancel()
	source := &cancelAfterRead{Store: store, cancel: cancel}
	cached := cache.NewMemory(time.Hour)
	s := guide.NewService(source, cached, guide.CacheAside)
	s.FillDelay = time.Hour
	if _, err := s.Get(cancelled, "1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("want cancellation, got %v", err)
	}
	if !source.called {
		t.Fatal("cancellation did not reach fill delay")
	}
	if _, err := cached.Get(ctx, "1"); !errors.Is(err, guide.ErrCacheMiss) {
		t.Fatal("cancelled refill modified cache")
	}
}
