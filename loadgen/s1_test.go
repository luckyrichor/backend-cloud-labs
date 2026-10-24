package loadgen

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestZipfFavoursLowRanksAndCoversSupport(t *testing.T) {
	z := NewZipf(1000, 0.99)
	r := rand.New(rand.NewPCG(1, 2))
	counts := make([]int, 1000)
	for range 200000 {
		counts[z.Sample(r)]++
	}
	if counts[0] < 5*counts[99] || counts[0] < counts[1] {
		t.Fatalf("rank 0 %d, rank 1 %d, rank 99 %d", counts[0], counts[1], counts[99])
	}
	// Expected share of rank 0 is 1/H(1000,0.99) ≈ 0.13.
	if share := float64(counts[0]) / 200000; share < 0.11 || share > 0.15 {
		t.Fatalf("rank 0 share %.3f", share)
	}
	if NewZipf(10, 0).Sample(r) > 9 {
		t.Fatal("out of range")
	}
}

func TestPlanIsDeterministicAndRespectsMix(t *testing.T) {
	pop := []uint32{7, 8, 9, 10}
	a, b := PlanS1(5, 10000, pop, 0.99, 0.95), PlanS1(5, 10000, pop, 0.99, 0.95)
	writes := 0
	for i := range a {
		if a[i] != b[i] {
			t.Fatal("same seed, different plan")
		}
		if a[i].write {
			writes++
		}
		if a[i].id < 7 || a[i].id > 10 {
			t.Fatal("id outside popularity table")
		}
	}
	if writes < 400 || writes > 600 {
		t.Fatalf("writes %d of 10000 at 95:5", writes)
	}
	if fmt.Sprint(PlanS1(6, 100, pop, 0.99, 0.95)) == fmt.Sprint(a[:100]) {
		t.Fatal("different seed, same plan")
	}
}

func TestStaleDefinitionUsesAcknowledgedWritesOnly(t *testing.T) {
	recs := []S1Record{
		{ID: 1, Write: true, Window: true, Issued: true, Planned: 0, Sent: 0, Done: 100, Status: 200, Version: 2},
		// sent before the ack arrived: not stale even though it returned v1
		{ID: 1, Window: true, Issued: true, Planned: 50, Sent: 50, Done: 60, Status: 200, Version: 1},
		// sent after the ack: v1 is stale, age 200ns → 1µs floor
		{ID: 1, Window: true, Issued: true, Planned: 300, Sent: 300, Done: 310, Status: 200, Version: 1, Source: "cache_unvalidated"},
		{ID: 1, Window: true, Issued: true, Planned: 400, Sent: 400, Done: 410, Status: 200, Version: 2, Source: "store"},
		// an id never written cannot be stale
		{ID: 2, Window: true, Issued: true, Planned: 500, Sent: 500, Done: 510, Status: 200, Version: 1},
		// failed reads are counted as failures, not as stale or fresh
		{ID: 1, Window: true, Issued: true, Planned: 600, Sent: 600, Done: 700, Status: 503},
		// warmup records are excluded from window statistics
		{ID: 1, Issued: true, Planned: 0, Sent: 0, Done: 5, Status: 200, Version: 1},
	}
	var res S1Result
	summarizeS1(&res, recs, 1)
	if res.StaleReads != 1 || res.Reads.Success != 4 || res.Reads.Failed != 1 || res.ReadsOfWrittenIDs != 3 || res.ClientCacheServed != 1 {
		t.Fatalf("%+v", res)
	}
	if res.StaleRatio != 0.25 || res.Writes.Count != 1 {
		t.Fatalf("ratio %v writes %d", res.StaleRatio, res.Writes.Count)
	}
}

// fakeE1 serves versions; with lag > 0, reads keep returning the previous version for that
// long after each write, like a cache that missed an invalidation.
type fakeE1 struct {
	mu       sync.Mutex
	versions map[string]int64
	changed  map[string]time.Time
	lag      time.Duration
}

func (f *fakeE1) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/items/")
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.Method {
	case http.MethodPut:
		if r.Header.Get("Authorization") != "Bearer t" {
			w.WriteHeader(401)
			return
		}
		f.versions[id]++
		f.changed[id] = time.Now()
	case http.MethodGet:
	}
	v := max(f.versions[id], 1)
	if r.Method == http.MethodGet && time.Since(f.changed[id]) < f.lag && v > 1 {
		v--
	}
	json.NewEncoder(w).Encode(map[string]any{"item": map[string]any{"id": id, "version": v}, "source": "cache_unvalidated"})
}

func runFake(t *testing.T, lag time.Duration) S1Result {
	t.Helper()
	fake := &fakeE1{versions: map[string]int64{}, changed: map[string]time.Time{}, lag: lag}
	admin := http.NewServeMux()
	scrapes := 0
	admin.HandleFunc("GET /metrics", func(w http.ResponseWriter, r *http.Request) {
		// Window start sees zeros, window end sees 9 hits and 1 miss; unrelated lines are ignored.
		n := scrapes
		scrapes++
		fmt.Fprintf(w, "bcl_e1_cache_hits_total %d\nbcl_e1_cache_misses_total %d\nbcl_e1_source_reads_total %d\nother 5\n", 9*n, n, 10*n)
	})
	admin.HandleFunc("GET /debug/latency", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"unit":"us"}`)) })
	admin.HandleFunc("GET /debug/strategy", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"mode":"fake"}`)) })
	biz, adm := httptest.NewServer(fake), httptest.NewServer(admin)
	defer biz.Close()
	defer adm.Close()
	res, recs, err := RunS1(context.Background(), S1Config{Target: biz.URL, Admin: adm.URL, Popularity: []uint32{1, 2, 3}, ZipfS: 0.99,
		ReadRatio: 0.8, Rate: 400, Warmup: 250 * time.Millisecond, Window: time.Second, Seed: 3, MaxInflight: 64, Timeout: time.Second, Authorization: "Bearer t"})
	if err != nil || len(recs) != 500 {
		t.Fatalf("records %d err %v", len(recs), err)
	}
	return res
}

func TestRunS1CountsStaleReadsOnlyWhenTheServerLags(t *testing.T) {
	fresh := runFake(t, 0)
	if !fresh.Valid || fresh.StaleReads != 0 || fresh.Reads.Failed+fresh.Writes.Failed != 0 || fresh.Reads.Count+fresh.Writes.Count != 400 {
		t.Fatalf("fresh server: %+v", fresh)
	}
	if fresh.Server["cache_hit_ratio"] != 0.9 {
		t.Fatalf("server hit ratio %v", fresh.Server["cache_hit_ratio"])
	}
	lagging := runFake(t, 30*time.Millisecond)
	if lagging.StaleReads == 0 || lagging.StaleAge.Max > 200_000 {
		t.Fatalf("lagging server: stale %d, age %+v", lagging.StaleReads, lagging.StaleAge)
	}
}
