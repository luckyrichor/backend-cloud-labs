package observe_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/cache"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/catalog"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/catalogtest"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/guide"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/observe"
)

func registry() (observe.Registry, map[string]*atomic.Uint64) {
	m := map[string]*atomic.Uint64{}
	return func(name string) *atomic.Uint64 { m[name] = &atomic.Uint64{}; return m[name] }, m
}

func TestCountersFollowStrategyOperations(t *testing.T) {
	reg, c := registry()
	ctx := context.Background()
	s := guide.NewService(observe.NewStore(catalogtest.NewStore(t), reg), observe.NewCache(cache.NewMemory(time.Hour), reg), guide.CacheAside)
	s.Put(ctx, catalog.Item{ID: "1", Title: "t"}) // source write + delete
	s.Get(ctx, "1")                               // miss, source read, set
	s.Get(ctx, "1")                               // hit
	want := map[string]uint64{"bcl_e1_source_writes_total": 1, "bcl_e1_cache_deletes_total": 1, "bcl_e1_cache_misses_total": 1,
		"bcl_e1_source_reads_total": 1, "bcl_e1_cache_sets_total": 1, "bcl_e1_cache_hits_total": 1, "bcl_e1_cache_errors_total": 0}
	for name, n := range want {
		if got := c[name].Load(); got != n {
			t.Errorf("%s = %d, want %d", name, got, n)
		}
	}
	// A rejected gated fill is counted separately from an applied one.
	v := guide.NewService(observe.NewStore(catalogtest.NewStore(t), reg), observe.NewCache(cache.NewMemory(time.Hour), reg), guide.Versioned)
	v.Put(ctx, catalog.Item{ID: "1", Title: "t"})
	v.Put(ctx, catalog.Item{ID: "1", Title: "t"})
	if c["bcl_e1_cache_fills_applied_total"].Load() != 2 || c["bcl_e1_cache_fills_rejected_total"].Load() != 0 {
		t.Fatalf("fills applied %d rejected %d", c["bcl_e1_cache_fills_applied_total"].Load(), c["bcl_e1_cache_fills_rejected_total"].Load())
	}
}

func TestLatencySnapshotAndReset(t *testing.T) {
	l := observe.NewLatency()
	h := l.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	for range 3 {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/items/1", nil))
	}
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/recommendations", nil)) // not an S1 metric
	read := func(path string) (get, put int64) {
		w := httptest.NewRecorder()
		l.Handler().ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		var snap struct {
			Unit     string
			Get, Put struct{ Counts []int64 }
		}
		if err := json.Unmarshal(w.Body.Bytes(), &snap); err != nil || snap.Unit != "us" {
			t.Fatalf("snapshot %s %v", w.Body.String(), err)
		}
		for _, n := range snap.Get.Counts {
			get += n
		}
		for _, n := range snap.Put.Counts {
			put += n
		}
		return
	}
	if g, p := read("/debug/latency?reset=1"); g != 3 || p != 0 {
		t.Fatalf("get %d put %d", g, p)
	}
	if g, _ := read("/debug/latency"); g != 0 {
		t.Fatalf("reset left %d samples", g)
	}
}

func TestFileTracerWritesOrderedLinesForOneKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace.jsonl")
	tr, err := observe.NewFileTracer("1", path)
	if err != nil {
		t.Fatal(err)
	}
	s := guide.NewService(catalogtest.NewStore(t), cache.NewMemory(time.Hour), guide.Versioned)
	s.Trace = tr
	ctx := context.Background()
	s.Put(ctx, catalog.Item{ID: "1", Title: "t"})
	s.Put(ctx, catalog.Item{ID: "2", Title: "t"})
	s.Get(ctx, "1")
	if err := tr.Close(); err != nil {
		t.Fatal(err)
	}
	f, _ := os.Open(path)
	defer f.Close()
	var events []string
	var last int64
	for sc := bufio.NewScanner(f); sc.Scan(); {
		var l struct {
			Op    uint64 `json:"op"`
			Event string `json:"event"`
			TNs   int64  `json:"t_ns"`
		}
		json.Unmarshal(sc.Bytes(), &l)
		if l.TNs < last {
			t.Fatal("timestamps go backwards")
		}
		last = l.TNs
		events = append(events, l.Event)
	}
	want := []string{"begin", "write_commit", "fill", "begin", "read_start", "cache_hit"}
	if len(events) != len(want) {
		t.Fatalf("events %v, want %v", events, want)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("events %v, want %v", events, want)
		}
	}
}

type projectedStore struct{ catalog.Store }

func (s projectedStore) GetVersion(ctx context.Context, id string) (int64, error) {
	v, err := s.Store.Get(ctx, id)
	return v.Version, err
}
func TestProjectionCountersMatchStrictVersionQueryDecisions(t *testing.T) {
	ctx := context.Background()
	base := catalogtest.NewStore(t)
	base.Put(ctx, catalog.Item{ID: "1", Title: "one"})
	reg, c := registry()
	store := observe.NewStore(projectedStore{base}, reg)
	s := guide.NewService(store, cache.NewMemory(time.Hour), guide.StrictVersion)
	for range 2 {
		if _, err := s.Get(ctx, "1"); err != nil {
			t.Fatal(err)
		}
	}
	base.Put(ctx, catalog.Item{ID: "1", Title: "two"})
	if _, err := s.Get(ctx, "1"); err != nil {
		t.Fatal(err)
	}
	if c["bcl_e1_source_reads_total"].Load() != 4 || c["bcl_e1_source_full_reads_total"].Load() != 2 || c["bcl_e1_source_version_reads_total"].Load() != 2 {
		t.Fatal("projection counts must include mismatch full reread")
	}
}
