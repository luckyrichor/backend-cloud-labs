package catalog_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/catalog"
)

func fixedClock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

func TestGetOnAnEmptyStoreReportsNotFound(t *testing.T) {
	store := catalog.NewMemoryStore()

	_, err := store.Get(context.Background(), "absent")

	if !errors.Is(err, catalog.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestPutAssignsVersionsSoTheCallerCannotForgetTo(t *testing.T) {
	store := catalog.NewMemoryStoreWithClock(fixedClock(time.Unix(1_700_000_000, 0)))
	ctx := context.Background()

	first, err := store.Put(ctx, catalog.Item{ID: "sku-1", Title: "kettle", PriceCent: 4990, Version: 99})
	if err != nil {
		t.Fatalf("first put: %v", err)
	}
	second, err := store.Put(ctx, catalog.Item{ID: "sku-1", Title: "kettle", PriceCent: 3990})
	if err != nil {
		t.Fatalf("second put: %v", err)
	}

	if first.Version != 1 {
		t.Errorf("first version = %d, want 1 (the caller's 99 must be ignored)", first.Version)
	}
	if second.Version != 2 {
		t.Errorf("second version = %d, want 2", second.Version)
	}
}

func TestGetReturnsTheLatestWrite(t *testing.T) {
	store := catalog.NewMemoryStore()
	ctx := context.Background()
	if _, err := store.Put(ctx, catalog.Item{ID: "sku-1", PriceCent: 4990}); err != nil {
		t.Fatalf("put: %v", err)
	}
	if _, err := store.Put(ctx, catalog.Item{ID: "sku-1", PriceCent: 3990}); err != nil {
		t.Fatalf("put: %v", err)
	}

	got, err := store.Get(ctx, "sku-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if got.PriceCent != 3990 || got.Version != 2 {
		t.Errorf("got price=%d version=%d, want 3990 / 2", got.PriceCent, got.Version)
	}
}

func TestCancelledContextIsRefusedBeforeTouchingState(t *testing.T) {
	store := catalog.NewMemoryStore()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := store.Put(ctx, catalog.Item{ID: "sku-1"}); !errors.Is(err, context.Canceled) {
		t.Errorf("put: want context.Canceled, got %v", err)
	}
	if _, err := store.Get(context.Background(), "sku-1"); !errors.Is(err, catalog.ErrNotFound) {
		t.Errorf("the refused put must not have stored anything, got %v", err)
	}
}

// Run with -race: the store is read concurrently by every request.
func TestConcurrentReadsAndWritesAreSafe(t *testing.T) {
	store := catalog.NewMemoryStore()
	ctx := context.Background()
	if _, err := store.Put(ctx, catalog.Item{ID: "sku-1"}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); _, _ = store.Get(ctx, "sku-1") }()
		go func() { defer wg.Done(); _, _ = store.Put(ctx, catalog.Item{ID: "sku-1"}) }()
	}
	wg.Wait()

	got, err := store.Get(ctx, "sku-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Version != 51 {
		t.Errorf("version = %d, want 51 (1 seed + 50 writes)", got.Version)
	}
}
