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

// recordingCache distinguishes the two cache primitives so the S1 branch arms
// prove which path a request actually took rather than only inspecting config.
type recordingCache struct {
	inner                *cache.Memory
	gated, sets, deletes int
}

func (c *recordingCache) Get(ctx context.Context, id string) (catalog.Item, error) {
	return c.inner.Get(ctx, id)
}

func (c *recordingCache) PutIfNewer(ctx context.Context, item catalog.Item) (bool, error) {
	c.gated++
	return c.inner.PutIfNewer(ctx, item)
}

func (c *recordingCache) Set(ctx context.Context, item catalog.Item) error {
	c.sets++
	return c.inner.Set(ctx, item)
}

func (c *recordingCache) Delete(ctx context.Context, id string) error {
	c.deletes++
	return c.inner.Delete(ctx, id)
}

func TestGateOnlyBranchGatesReadsAndDeletesOnWrite(t *testing.T) {
	ctx := context.Background()
	c := &recordingCache{inner: cache.NewMemory(time.Hour)}
	s := guide.NewService(catalogtest.NewStore(t), c, guide.CacheAside)
	s.SharedNamespace = true
	s.GatedAside = true
	if _, err := s.Put(ctx, catalog.Item{ID: "1", Title: "t", PriceCent: 1, Stock: 1}); err != nil {
		t.Fatal(err)
	}
	if c.gated != 0 || c.sets != 0 || c.deletes != 1 {
		t.Fatalf("gate-only write must delete, not fill: gated=%d sets=%d deletes=%d", c.gated, c.sets, c.deletes)
	}
	first, err := s.Get(ctx, "1")
	if err != nil || first.Source != "store" {
		t.Fatalf("cache miss should read the source: %+v %v", first, err)
	}
	if c.gated != 1 || c.sets != 0 {
		t.Fatalf("gate-only read fill must use PutIfNewer: gated=%d sets=%d", c.gated, c.sets)
	}
	second, err := s.Get(ctx, "1")
	if err != nil || second.Source != "cache_unvalidated" {
		t.Fatalf("second read should hit the gated entry: %+v %v", second, err)
	}
}

func TestWriteOnlyBranchFillsUngatedOnReadAndWrite(t *testing.T) {
	ctx := context.Background()
	c := &recordingCache{inner: cache.NewMemory(time.Hour)}
	s := guide.NewService(catalogtest.NewStore(t), c, guide.Versioned)
	s.SharedNamespace = true
	s.UngatedFills = true
	if _, err := s.Put(ctx, catalog.Item{ID: "1", Title: "t", PriceCent: 1, Stock: 1}); err != nil {
		t.Fatal(err)
	}
	if c.gated != 0 || c.sets != 1 || c.deletes != 0 {
		t.Fatalf("write-only put must use plain SET: gated=%d sets=%d deletes=%d", c.gated, c.sets, c.deletes)
	}
	if err := c.inner.Delete(ctx, "1"); err != nil {
		t.Fatal(err)
	}
	read, err := s.Get(ctx, "1")
	if err != nil || read.Source != "store" {
		t.Fatalf("cache miss should read the source: %+v %v", read, err)
	}
	if c.gated != 0 || c.sets != 2 {
		t.Fatalf("write-only read fill must use plain SET: gated=%d sets=%d", c.gated, c.sets)
	}
}

func TestStrategyDescriptorsReportBranchPaths(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mode    guide.ReadMode
		gated   bool
		ungated bool
		read    bool
		write   string
	}{
		{"A", guide.CacheAside, false, false, false, "delete"},
		{"B", guide.Versioned, false, false, true, "gated-fill"},
		{"B-off", guide.Versioned, false, true, false, "plain-fill"},
		{"B-gate-only", guide.CacheAside, true, false, true, "delete"},
		{"B-write-only", guide.Versioned, false, true, false, "plain-fill"},
	} {
		s := guide.Service{Mode: tc.mode, GatedAside: tc.gated, UngatedFills: tc.ungated}
		if got := s.ReadFillGated(); got != tc.read || s.WritePath() != tc.write {
			t.Fatalf("%s: read_fill_gated=%t write_path=%s", tc.name, got, s.WritePath())
		}
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
