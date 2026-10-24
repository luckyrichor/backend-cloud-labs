package guide_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/cache"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/catalog"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/catalogtest"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/guide"
)

// lateFill replays the S1 race on one key: a reader misses the cache and reads v1 from
// the source, then pauses before filling; a writer commits v2 and runs its cache step;
// the reader resumes and fills. It returns what the next read serves.
func lateFill(t *testing.T, mode guide.ReadMode, ungated bool) guide.Result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	base := catalogtest.NewStore(t)
	if _, err := base.Put(ctx, catalog.Item{ID: "1", Title: "t", PriceCent: 100, Stock: 1}); err != nil {
		t.Fatal(err)
	}
	gated := &gatedStore{Store: base, entered: make(chan struct{}), release: make(chan struct{})}
	s := guide.NewService(gated, cache.NewMemory(time.Hour), mode)
	s.UngatedFills = ungated
	done := make(chan error, 1)
	go func() { _, err := s.Get(ctx, "1"); done <- err }() // reads v1, then waits
	<-gated.entered
	written, err := s.Put(ctx, catalog.Item{ID: "1", Title: "t", PriceCent: 200, Stock: 1})
	if err != nil || written.Item.Version != 2 {
		t.Fatalf("write %+v %v", written, err)
	}
	close(gated.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	next, err := s.Get(ctx, "1")
	if err != nil {
		t.Fatal(err)
	}
	return next
}

func TestCacheAsideLateFillLeavesStaleValue(t *testing.T) {
	r := lateFill(t, guide.CacheAside, false)
	// The textbook cache-aside race: DEL ran before the late SET, so v1 sits in the cache.
	if r.Item.Version != 1 || r.Source != "cache_unvalidated" {
		t.Fatalf("want stale v1 from cache, got v%d from %s", r.Item.Version, r.Source)
	}
}

func TestVersionedGateRejectsLateFill(t *testing.T) {
	r := lateFill(t, guide.Versioned, false)
	if r.Item.Version != 2 {
		t.Fatalf("gated fill let v1 overwrite v2: got v%d from %s", r.Item.Version, r.Source)
	}
}

// Reverse validation: with the gate off, B must fall back to the stale outcome.
func TestVersionedWithoutGateGoesStaleAgain(t *testing.T) {
	r := lateFill(t, guide.Versioned, true)
	if r.Item.Version != 1 || r.Source != "cache_unvalidated" {
		t.Fatalf("ungated arm should serve stale v1, got v%d from %s", r.Item.Version, r.Source)
	}
}

func TestStrictAndCacheFirstSurviveLateFill(t *testing.T) {
	for _, mode := range []guide.ReadMode{guide.Strict, guide.CacheFirst} {
		if r := lateFill(t, mode, false); r.Item.Version != 2 {
			t.Fatalf("%s served v%d", mode, r.Item.Version)
		}
	}
}

func TestCacheAsideWriteDeletesAndReadFills(t *testing.T) {
	ctx := context.Background()
	c := cache.NewMemory(time.Hour)
	s := guide.NewService(catalogtest.NewStore(t), c, guide.CacheAside)
	if _, err := s.Put(ctx, catalog.Item{ID: "1", Title: "t", PriceCent: 1, Stock: 1}); err != nil {
		t.Fatal(err)
	}
	first, _ := s.Get(ctx, "1")
	second, _ := s.Get(ctx, "1")
	if first.Source != "store" || second.Source != "cache_unvalidated" {
		t.Fatalf("miss should fill: %s then %s", first.Source, second.Source)
	}
	if _, err := s.Put(ctx, catalog.Item{ID: "1", Title: "t", PriceCent: 2, Stock: 1}); err != nil {
		t.Fatal(err)
	}
	after, _ := s.Get(ctx, "1")
	if after.Source != "store" || after.Item.Version != 2 {
		t.Fatalf("write should have deleted the entry: v%d from %s", after.Item.Version, after.Source)
	}
}

// gateOnlyCache has PutIfNewer but no Set/Delete.
type gateOnlyCache struct{ guide.Cache }

func TestUngatedStrategiesNeedSetAndDelete(t *testing.T) {
	ctx := context.Background()
	store := catalogtest.NewStore(t)
	store.Put(ctx, catalog.Item{ID: "1", Title: "t"})
	for _, c := range []struct {
		mode    guide.ReadMode
		ungated bool
	}{{guide.CacheAside, false}, {guide.Versioned, true}} {
		s := guide.NewService(store, gateOnlyCache{cache.NewMemory(time.Hour)}, c.mode)
		s.UngatedFills = c.ungated
		// The source write still commits and is reported as degraded, never as failed.
		w, err := s.Put(ctx, catalog.Item{ID: "1", Title: "t"})
		if err != nil || !w.Degraded {
			t.Fatalf("%s: put %+v %v", c.mode, w, err)
		}
		r, err := s.Get(ctx, "1")
		if err != nil || !r.Degraded || r.Source != "store" {
			t.Fatalf("%s: get %+v %v", c.mode, r, err)
		}
	}
	// The gated arm keeps working with such a cache.
	s := guide.NewService(store, gateOnlyCache{cache.NewMemory(time.Hour)}, guide.Versioned)
	if w, err := s.Put(ctx, catalog.Item{ID: "1", Title: "t"}); err != nil || w.Degraded {
		t.Fatalf("versioned with gate: %+v %v", w, err)
	}
}

func TestZeroModeIsStrict(t *testing.T) {
	ctx := context.Background()
	store := &countingStore{Store: catalogtest.NewStore(t)}
	s := guide.NewService(store, cache.NewMemory(time.Hour), "")
	s.Put(ctx, catalog.Item{ID: "1", Title: "t"})
	s.Get(ctx, "1")
	r, _ := s.Get(ctx, "1")
	if r.Source != "cache_validated" || store.reads != 2 {
		t.Fatalf("default should validate every read against the source: %s, %d source reads", r.Source, store.reads)
	}
}

type recordingTracer struct {
	mu     sync.Mutex
	next   uint64
	events []string
}

func (r *recordingTracer) Begin(id, kind string) uint64 {
	if id != "1" {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.next++
	return r.next
}

func (r *recordingTracer) Event(op uint64, id, name string, version int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, fmt.Sprintf("%d:%s:v%d", op, name, version))
}

func TestTracerRecordsOnlyTracedKeys(t *testing.T) {
	ctx := context.Background()
	tr := &recordingTracer{}
	s := guide.NewService(catalogtest.NewStore(t), cache.NewMemory(time.Hour), guide.CacheAside)
	s.Trace = tr
	s.Put(ctx, catalog.Item{ID: "1", Title: "t"})
	s.Put(ctx, catalog.Item{ID: "2", Title: "t"})
	s.Get(ctx, "1")
	s.Get(ctx, "1")
	want := []string{"1:write_commit:v1", "1:delete:v1", "2:read_start:v0", "2:store_read:v1", "2:fill:v1", "3:read_start:v0", "3:cache_hit:v1"}
	if fmt.Sprint(tr.events) != fmt.Sprint(want) {
		t.Fatalf("events\n got %v\nwant %v", tr.events, want)
	}
}
