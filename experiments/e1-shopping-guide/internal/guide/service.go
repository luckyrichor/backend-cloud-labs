// Package guide compares strict and cache-first reads with monotonic fills.
package guide

import (
	"context"
	"errors"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/catalog"
	"sort"
	"sync"
)

var ErrCacheMiss = errors.New("cache miss")
var ErrRepairQueueRequired = errors.New("cache-first requires a shared repair queue; use NewService")

type Cache interface {
	Get(context.Context, string) (catalog.Item, error)
	PutIfNewer(context.Context, catalog.Item) (bool, error)
}
type Result struct {
	Item     catalog.Item `json:"item"`
	Source   string       `json:"source"`
	Degraded bool         `json:"degraded"`
}
type ReadMode string

const (
	Strict     ReadMode = "strict"
	CacheFirst ReadMode = "cache-first"
)

// Empty mode preserves strict behavior. CacheFirst trades freshness for source load.
func ValidReadMode(mode ReadMode) bool { return mode == "" || mode == Strict || mode == CacheFirst }

type Service struct {
	Mode       ReadMode
	Store      catalog.Store
	Cache      Cache
	Repairs    *RepairQueue
	generation uint64
}

// NewService enables bounded local read-repair; share this value across requests.
func NewService(store catalog.Store, cache Cache, mode ReadMode) Service {
	return Service{Store: store, Cache: cache, Mode: mode, Repairs: NewRepairQueue(1024)}
}

func (s Service) Get(ctx context.Context, id string) (Result, error) {
	s = s.snapshotCache()
	if s.Mode == CacheFirst && s.Repairs == nil {
		return Result{}, ErrRepairQueueRequired
	}
	if !ValidReadMode(s.Mode) {
		return Result{}, errors.New("invalid cache read mode")
	}
	if s.Mode == CacheFirst && !s.Repairs.pending(id) {
		cached, err := s.Cache.Get(ctx, id)
		if err == nil {
			return Result{cached, "cache_unvalidated", false}, nil
		}
		authoritative, sourceErr := s.Store.Get(ctx, id)
		if sourceErr != nil {
			return Result{}, sourceErr
		}
		_, fillErr := s.Cache.PutIfNewer(ctx, authoritative)
		return Result{authoritative, "store", !errors.Is(err, ErrCacheMiss) || fillErr != nil}, nil
	}
	// Strict mode validates source revision even on hits, trading DB load for correctness.
	authoritative, err := s.Store.Get(ctx, id)
	if err != nil {
		return Result{}, err
	}
	cached, cacheErr := s.Cache.Get(ctx, id)
	if cacheErr == nil && cached.Version == authoritative.Version {
		s.Repairs.clearFor(s.generation, id, authoritative.Version)
		return Result{cached, "cache_validated", false}, nil
	}
	_, fillErr := s.Cache.PutIfNewer(ctx, authoritative)
	if fillErr == nil {
		s.Repairs.clearFor(s.generation, id, authoritative.Version)
	}
	degraded := (cacheErr != nil && !errors.Is(cacheErr, ErrCacheMiss)) || fillErr != nil
	return Result{authoritative, "store", degraded}, nil
}
func (s Service) Put(ctx context.Context, item catalog.Item) (Result, error) {
	s = s.snapshotCache()
	if s.Mode == CacheFirst && s.Repairs == nil {
		return Result{}, ErrRepairQueueRequired
	}
	authoritative, err := s.Store.Put(ctx, item)
	if err != nil {
		return Result{}, err
	}
	_, err = s.Cache.PutIfNewer(ctx, authoritative)
	s.Repairs.finishWrite(s.generation, authoritative.ID, authoritative.Version, err == nil)
	// Cache errors never disguise an already-committed source write as a failed write.
	return Result{authoritative, "store", err != nil}, nil
}
func (s Service) Recommend(ctx context.Context, ids []string, budgetCent int64) ([]Result, error) {
	// At most eight workers per recommendation; indexed slots preserve the
	// input error order and all workers finish before returning or sorting.
	unique := make([]string, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			unique = append(unique, id)
		}
	}
	rows := make([]Result, len(unique))
	errs := make([]error, len(unique))
	jobs := make(chan int, len(unique))
	for i := range unique {
		jobs <- i
	}
	close(jobs)
	var wg sync.WaitGroup
	for worker := 0; worker < min(8, len(unique)); worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				rows[i], errs[i] = s.Get(ctx, unique[i])
			}
		}()
	}
	wg.Wait()
	results := make([]Result, 0, len(unique))
	for i, result := range rows {
		if errors.Is(errs[i], catalog.ErrNotFound) {
			continue
		}
		if errs[i] != nil {
			return nil, errs[i]
		}
		if result.Item.Stock > 0 && result.Item.PriceCent <= budgetCent {
			results = append(results, result)
		}
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].Item.PriceCent == results[j].Item.PriceCent {
			return results[i].Item.ID < results[j].Item.ID
		}
		return results[i].Item.PriceCent < results[j].Item.PriceCent
	})
	return results, nil
}
