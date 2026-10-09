package guide_test

import (
	"context"
	"fmt"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/cache"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/catalog"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/guide"
	"testing"
	"time"
)

type countingStore struct {
	catalog.Store
	reads int
}

func (s *countingStore) Get(ctx context.Context, id string) (catalog.Item, error) {
	s.reads++
	return s.Store.Get(ctx, id)
}

func TestReadModesExposeFreshnessSourceLoadTradeoff(t *testing.T) {
	for _, mode := range []guide.ReadMode{guide.Strict, guide.CacheFirst} {
		t.Run(string(mode), func(t *testing.T) {
			ctx := context.Background()
			store := &countingStore{Store: catalog.NewMemoryStore()}
			c := cache.NewMemory(time.Minute)
			service := guide.Service{Store: store, Cache: c, Mode: mode}
			_, _ = service.Put(ctx, catalog.Item{ID: "sku", PriceCent: 100, Stock: 1})
			// Committed write during cache outage leaves the old cached revision behind.
			offline := guide.Service{Store: store, Cache: broken{}, Mode: mode}
			written, err := offline.Put(ctx, catalog.Item{ID: "sku", PriceCent: 200, Stock: 1})
			if err != nil || !written.Degraded {
				t.Fatal(written, err)
			}
			result, err := service.Get(ctx, "sku")
			if err != nil {
				t.Fatal(err)
			}
			wantVersion, wantReads := int64(2), 1
			if mode == guide.CacheFirst {
				wantVersion, wantReads = 1, 0
			}
			if result.Item.Version != wantVersion || store.reads != wantReads {
				t.Fatal(result, store.reads)
			}
			// Expired cache forces source refresh in either mode.
			c.TTL = -time.Second
			_, _ = c.PutIfNewer(ctx, result.Item)
			refreshed, err := service.Get(ctx, "sku")
			if err != nil || refreshed.Item.Version != 2 || store.reads != wantReads+1 {
				t.Fatal(refreshed, err, store.reads)
			}
			unavailable := guide.Service{Store: store, Cache: broken{}, Mode: mode}
			fallback, err := unavailable.Get(ctx, "sku")
			if err != nil || !fallback.Degraded || fallback.Item.Version != 2 {
				t.Fatal(fallback, err)
			}
		})
	}
}

func TestInvalidReadModeRejected(t *testing.T) {
	s := guide.Service{Mode: "typo"}
	if _, err := s.Get(context.Background(), "sku"); err == nil {
		t.Fatal("invalid mode accepted")
	}
}

func BenchmarkReadModes(b *testing.B) {
	for _, mode := range []guide.ReadMode{guide.Strict, guide.CacheFirst} {
		b.Run(fmt.Sprint(mode), func(b *testing.B) {
			ctx := context.Background()
			store := &countingStore{Store: catalog.NewMemoryStore()}
			s := guide.NewService(store, cache.NewMemory(time.Hour), mode)
			_, _ = s.Put(ctx, catalog.Item{ID: "sku", PriceCent: 100, Stock: 1})
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				r, err := s.Get(ctx, "sku")
				if err != nil || r.Item.Version != 1 {
					b.Fatal(r, err)
				}
			}
			b.StopTimer()
			b.ReportMetric(float64(store.reads)/float64(b.N), "source-reads/op")
		})
	}
}
