// Package guide demonstrates revision-validated cache reads with monotonic fills.
package guide

import (
	"context"
	"errors"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/catalog"
)

var ErrCacheMiss = errors.New("cache miss")

type Cache interface {
	Get(context.Context, string) (catalog.Item, error)
	PutIfNewer(context.Context, catalog.Item) (bool, error)
}
type Result struct {
	Item     catalog.Item `json:"item"`
	Source   string       `json:"source"`
	Degraded bool         `json:"degraded"`
}
type Service struct {
	Store catalog.Store
	Cache Cache
}

func (s Service) Get(ctx context.Context, id string) (Result, error) {
	// Strict mode validates source revision even on hits, trading DB load for correctness.
	authoritative, err := s.Store.Get(ctx, id)
	if err != nil {
		return Result{}, err
	}
	cached, cacheErr := s.Cache.Get(ctx, id)
	if cacheErr == nil && cached.Version == authoritative.Version {
		return Result{cached, "cache_validated", false}, nil
	}
	_, fillErr := s.Cache.PutIfNewer(ctx, authoritative)
	degraded := (cacheErr != nil && !errors.Is(cacheErr, ErrCacheMiss)) || fillErr != nil
	return Result{authoritative, "store", degraded}, nil
}
func (s Service) Put(ctx context.Context, item catalog.Item) (Result, error) {
	authoritative, err := s.Store.Put(ctx, item)
	if err != nil {
		return Result{}, err
	}
	_, err = s.Cache.PutIfNewer(ctx, authoritative)
	// Cache errors never disguise an already-committed source write as a failed write.
	return Result{authoritative, "store", err != nil}, nil
}
func (s Service) Recommend(ctx context.Context, ids []string, budgetCent int64) ([]Result, error) {
	results := make([]Result, 0)
	for _, id := range ids {
		result, err := s.Get(ctx, id)
		if errors.Is(err, catalog.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if result.Item.Stock > 0 && result.Item.PriceCent <= budgetCent {
			results = append(results, result)
		}
	}
	return results, nil
}
