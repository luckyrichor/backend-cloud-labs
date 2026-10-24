// Package observe adds the S1 measurements around an unchanged guide.Service: source and
// cache operation counters, server-side latency histograms, and a one-key event timeline.
// Everything here is exported on the admin listener only.
package observe

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	hdr "github.com/HdrHistogram/hdrhistogram-go"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/catalog"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/guide"
)

// Registry creates named counters (telemetry.Metrics.Counter satisfies it via a closure).
type Registry func(name string) *atomic.Uint64

// Store counts source operations; source reads per second is S1's "MySQL read QPS".
type Store struct {
	catalog.Store
	Reads, Writes, Errors *atomic.Uint64
}

func NewStore(s catalog.Store, reg Registry) *Store {
	return &Store{s, reg("bcl_e1_source_reads_total"), reg("bcl_e1_source_writes_total"), reg("bcl_e1_source_errors_total")}
}

func (s *Store) Get(ctx context.Context, id string) (catalog.Item, error) {
	s.Reads.Add(1)
	item, err := s.Store.Get(ctx, id)
	if err != nil && !errors.Is(err, catalog.ErrNotFound) {
		s.Errors.Add(1)
	}
	return item, err
}

func (s *Store) Put(ctx context.Context, item catalog.Item) (catalog.Item, error) {
	s.Writes.Add(1)
	out, err := s.Store.Put(ctx, item)
	if err != nil {
		s.Errors.Add(1)
	}
	return out, err
}

// Cache counts cache outcomes. It always offers Set/Delete and forwards them when the
// wrapped cache has them, so wrapping never changes which strategies are available.
type Cache struct {
	guide.Cache
	Hits, Misses, Errors, FillsApplied, FillsRejected, Sets, Deletes *atomic.Uint64
}

func NewCache(c guide.Cache, reg Registry) *Cache {
	return &Cache{c, reg("bcl_e1_cache_hits_total"), reg("bcl_e1_cache_misses_total"), reg("bcl_e1_cache_errors_total"),
		reg("bcl_e1_cache_fills_applied_total"), reg("bcl_e1_cache_fills_rejected_total"),
		reg("bcl_e1_cache_sets_total"), reg("bcl_e1_cache_deletes_total")}
}

func (c *Cache) Get(ctx context.Context, id string) (catalog.Item, error) {
	item, err := c.Cache.Get(ctx, id)
	switch {
	case err == nil:
		c.Hits.Add(1)
	case errors.Is(err, guide.ErrCacheMiss):
		c.Misses.Add(1)
	default:
		c.Errors.Add(1)
	}
	return item, err
}

func (c *Cache) PutIfNewer(ctx context.Context, item catalog.Item) (bool, error) {
	ok, err := c.Cache.PutIfNewer(ctx, item)
	switch {
	case err != nil:
		c.Errors.Add(1)
	case ok:
		c.FillsApplied.Add(1)
	default:
		c.FillsRejected.Add(1)
	}
	return ok, err
}

func (c *Cache) Set(ctx context.Context, item catalog.Item) error {
	u, ok := c.Cache.(guide.UngatedCache)
	if !ok {
		return guide.ErrUngatedCacheRequired
	}
	err := u.Set(ctx, item)
	c.count(err, c.Sets)
	return err
}

func (c *Cache) Delete(ctx context.Context, id string) error {
	u, ok := c.Cache.(guide.UngatedCache)
	if !ok {
		return guide.ErrUngatedCacheRequired
	}
	err := u.Delete(ctx, id)
	c.count(err, c.Deletes)
	return err
}

func (c *Cache) count(err error, ok *atomic.Uint64) {
	if err != nil {
		c.Errors.Add(1)
	} else {
		ok.Add(1)
	}
}

// Latency records server-side request latency per method in microseconds with the same
// HdrHistogram settings as loadgen (1 µs … 600 s, 3 significant digits), so the two can
// be reported side by side. The window is whatever lies between two resets.
type Latency struct {
	mu       sync.Mutex
	get, put *hdr.Histogram
	since    time.Time
}

func NewLatency() *Latency {
	return &Latency{get: hdr.New(1, 600_000_000, 3), put: hdr.New(1, 600_000_000, 3), since: time.Now().UTC()}
}

// Wrap measures GET and PUT on the business handler; other methods are not S1 metrics.
func (l *Latency) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		us := max(1, time.Since(start).Microseconds())
		l.mu.Lock()
		switch r.Method {
		case http.MethodGet:
			l.get.RecordValue(us)
		case http.MethodPut:
			l.put.RecordValue(us)
		}
		l.mu.Unlock()
	})
}

type latencySnapshot struct {
	Unit        string        `json:"unit"`
	WindowStart time.Time     `json:"window_start"`
	WindowEnd   time.Time     `json:"window_end"`
	Get         *hdr.Snapshot `json:"get"`
	Put         *hdr.Snapshot `json:"put"`
}

// Handler serves GET /debug/latency; ?reset=1 starts a new window atomically with the read.
func (l *Latency) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		now := time.Now().UTC()
		l.mu.Lock()
		snap := latencySnapshot{"us", l.since, now, l.get.Export(), l.put.Export()}
		if r.URL.Query().Get("reset") == "1" {
			l.get.Reset()
			l.put.Reset()
			l.since = now
		}
		l.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(snap)
	})
}

// FileTracer appends one JSON line per event of a single traced key. t_ns is monotonic
// time since the tracer started, so ordering inside one process is exact.
type FileTracer struct {
	key   string
	start time.Time
	mu    sync.Mutex
	f     *os.File
	w     *bufio.Writer
	next  atomic.Uint64
}

func NewFileTracer(key, path string) (*FileTracer, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	return &FileTracer{key: key, start: time.Now(), f: f, w: bufio.NewWriter(f)}, nil
}

type traceLine struct {
	Op      uint64 `json:"op"`
	Kind    string `json:"kind,omitempty"`
	Event   string `json:"event"`
	Version int64  `json:"version,omitempty"`
	TNs     int64  `json:"t_ns"`
}

func (t *FileTracer) Begin(id, kind string) uint64 {
	if id != t.key {
		return 0
	}
	op := t.next.Add(1)
	t.write(traceLine{Op: op, Kind: kind, Event: "begin"})
	return op
}

func (t *FileTracer) Event(op uint64, id, name string, version int64) {
	t.write(traceLine{Op: op, Event: name, Version: version})
}

func (t *FileTracer) write(l traceLine) {
	l.TNs = time.Since(t.start).Nanoseconds()
	b, _ := json.Marshal(l)
	t.mu.Lock()
	t.w.Write(append(b, '\n'))
	t.mu.Unlock()
}

// Flush is called periodically and on shutdown; events stay in memory until then.
func (t *FileTracer) Flush() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.w.Flush()
}

func (t *FileTracer) Close() error {
	return errors.Join(t.Flush(), t.f.Close())
}
