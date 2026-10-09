package guide_test

import (
	"context"
	"fmt"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/cache"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/catalog"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/guide"
	"sync/atomic"
	"testing"
	"time"
)

type gatedStore struct {
	catalog.Store
	entered chan struct{}
	release chan struct{}
	once    atomic.Bool
}

func (g *gatedStore) Get(ctx context.Context, id string) (catalog.Item, error) {
	item, err := g.Store.Get(ctx, id)
	if g.once.CompareAndSwap(false, true) {
		close(g.entered)
		select {
		case <-g.release:
		case <-ctx.Done():
			return catalog.Item{}, ctx.Err()
		}
	}
	return item, err
}
func TestLateSourceReadFillsOnlyCapturedGeneration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	store := &gatedStore{Store: catalog.NewMemoryStore(), entered: make(chan struct{}), release: make(chan struct{})}
	_, _ = store.Put(ctx, catalog.Item{ID: "a", PriceCent: 100})
	c := cache.NewMemory(time.Hour)
	s := guide.Service{Store: store, Cache: c, Mode: guide.CacheFirst, Repairs: guide.NewRepairQueue(1)}
	done := make(chan error, 1)
	go func() { _, err := s.Get(ctx, "a"); done <- err }()
	select {
	case <-store.entered:
	case <-ctx.Done():
		t.Fatal("reader did not pause")
	}
	fault := s
	fault.Cache = &switchCache{Cache: c, offline: true}
	_, _ = fault.Put(ctx, catalog.Item{ID: "a", PriceCent: 200})
	_, _ = fault.Put(ctx, catalog.Item{ID: "b", PriceCent: 300})
	close(store.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		r, err := s.Get(ctx, "a")
		if err != nil || r.Item.PriceCent != 200 {
			t.Fatal("late fill polluted new generation", r, err)
		}
	}
}

type parallelStore struct {
	catalog.Store
	entered chan struct{}
	release chan struct{}
	active  atomic.Int32
	peak    atomic.Int32
}

func (s *parallelStore) Get(ctx context.Context, id string) (catalog.Item, error) {
	active := s.active.Add(1)
	defer s.active.Add(-1)
	for peak := s.peak.Load(); active > peak; peak = s.peak.Load() {
		if s.peak.CompareAndSwap(peak, active) {
			break
		}
	}
	s.entered <- struct{}{}
	select {
	case <-s.release:
	case <-ctx.Done():
		return catalog.Item{}, ctx.Err()
	}
	return s.Store.Get(ctx, id)
}
func TestRecommendationsBoundConcurrencyAndJoinOnCancellation(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelled), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			store := &parallelStore{Store: catalog.NewMemoryStore(), entered: make(chan struct{}, 100), release: make(chan struct{})}
			ids := []string{}
			for i := 0; i < 20; i++ {
				id := fmt.Sprintf("%02d", i)
				ids = append(ids, id)
				_, _ = store.Put(ctx, catalog.Item{ID: id, PriceCent: int64(20 - i), Stock: 1})
			}
			ids = append(ids, ids[0])
			s := guide.NewService(store, cache.NewMemory(time.Hour), guide.Strict)
			done := make(chan error, 1)
			go func() {
				rows, err := s.Recommend(ctx, ids, 100)
				if err == nil && (len(rows) != 20 || rows[0].Item.ID != "19") {
					err = fmt.Errorf("bad ordered rows: %v", rows)
				}
				done <- err
			}()
			for i := 0; i < 8; i++ {
				select {
				case <-store.entered:
				case <-ctx.Done():
					t.Fatal("workers did not run in parallel")
				}
			}
			if cancelled {
				cancel()
			} else {
				close(store.release)
			}
			select {
			case err := <-done:
				if cancelled && err != context.Canceled {
					t.Fatal(err)
				}
				if !cancelled && err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("workers did not join")
			}
			if store.peak.Load() != 8 || store.active.Load() != 0 {
				t.Fatal(store.peak.Load(), store.active.Load())
			}
		})
	}
}

type gatedPutStore struct {
	catalog.Store
	entered chan struct{}
	release chan struct{}
}

func (s *gatedPutStore) Put(ctx context.Context, item catalog.Item) (catalog.Item, error) {
	close(s.entered)
	select {
	case <-s.release:
	case <-ctx.Done():
		return catalog.Item{}, ctx.Err()
	}
	return s.Store.Put(ctx, item)
}
func TestSourceWriteAcrossRotationInvalidatesNewGenerationEvenIfOldFillSucceeded(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	base := catalog.NewMemoryStore()
	_, _ = base.Put(ctx, catalog.Item{ID: "x", PriceCent: 100})
	blocked := &gatedPutStore{Store: base, entered: make(chan struct{}), release: make(chan struct{})}
	c := cache.NewMemory(time.Hour)
	s := guide.Service{Store: blocked, Cache: c, Mode: guide.CacheFirst, Repairs: guide.NewRepairQueue(1)}
	done := make(chan error, 1)
	go func() { _, err := s.Put(ctx, catalog.Item{ID: "x", PriceCent: 200}); done <- err }()
	select {
	case <-blocked.entered:
	case <-ctx.Done():
		t.Fatal("write did not capture old generation")
	}
	fault := s
	fault.Store = base
	fault.Cache = &switchCache{Cache: c, offline: true}
	_, _ = fault.Put(ctx, catalog.Item{ID: "a", PriceCent: 300})
	_, _ = fault.Put(ctx, catalog.Item{ID: "b", PriceCent: 400})
	r, err := s.Get(ctx, "x")
	if err != nil || r.Item.PriceCent != 100 {
		t.Fatal(r, err)
	}
	close(blocked.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	r, err = s.Get(ctx, "x")
	if err != nil || r.Item.PriceCent != 200 || r.Source != "store" {
		t.Fatal("old writer missed current invalidation", r, err)
	}
	r, err = s.Get(ctx, "x")
	if err != nil || r.Item.PriceCent != 200 || r.Source != "cache_unvalidated" {
		t.Fatal("repair did not restore hits", r, err)
	}
}
