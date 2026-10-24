package guide

import (
	"context"
	"errors"

	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/catalog"
)

// S1 compares four read/write strategies against the same source and cache (plan §E S1):
//
//	A CacheAside  read: hit → serve; miss → source → plain SET.     write: source → DEL.
//	B Versioned   read: hit → serve; miss → source → gated fill.    write: source → gated fill.
//	C Strict      read: source version first, cache only if equal.  write: source → gated fill.
//	D CacheFirst  B plus the local repair queue and cache generations (the pre-S1 behaviour).
//
// B and D differ only when a cache write fails: D marks the key so later reads bypass the
// stale entry, B leaves it until TTL. Without cache faults they should behave alike.
const (
	CacheAside ReadMode = "cache-aside"
	Versioned  ReadMode = "versioned"
)

// ErrUngatedCacheRequired: cache-aside and the ungated arm need Set/Delete.
var ErrUngatedCacheRequired = errors.New("strategy needs a cache with Set and Delete")

// UngatedCache is the plain SET/DEL pair a textbook cache-aside uses.
type UngatedCache interface {
	Set(context.Context, catalog.Item) error
	Delete(context.Context, string) error
}

func (c cacheView) Set(ctx context.Context, item catalog.Item) error {
	u, ok := c.Cache.(UngatedCache)
	if !ok {
		return ErrUngatedCacheRequired
	}
	item.ID = c.prefix + item.ID
	return u.Set(ctx, item)
}

func (c cacheView) Delete(ctx context.Context, id string) error {
	u, ok := c.Cache.(UngatedCache)
	if !ok {
		return ErrUngatedCacheRequired
	}
	return u.Delete(ctx, c.prefix+id)
}

func (s Service) ungated() (UngatedCache, error) {
	u, ok := s.Cache.(UngatedCache)
	if !ok {
		return nil, ErrUngatedCacheRequired
	}
	if v, isView := s.Cache.(cacheView); isView {
		if _, ok := v.Cache.(UngatedCache); !ok {
			return nil, ErrUngatedCacheRequired
		}
	}
	return u, nil
}

// fill writes a source copy into the cache. Versioned fills go through the version gate
// unless UngatedFills is set — the reverse-validation switch that turns B's gate off.
func (s Service) fill(ctx context.Context, item catalog.Item, gated bool) error {
	if gated {
		_, err := s.Cache.PutIfNewer(ctx, item)
		return err
	}
	u, err := s.ungated()
	if err != nil {
		return err
	}
	return u.Set(ctx, item)
}

// readThrough serves a hit unvalidated, otherwise reads the source and fills.
func (s Service) readThrough(ctx context.Context, op uint64, id string, gated bool) (Result, error) {
	cached, err := s.Cache.Get(ctx, id)
	if err == nil {
		s.event(op, id, "cache_hit", cached.Version)
		return Result{cached, "cache_unvalidated", false}, nil
	}
	authoritative, sourceErr := s.Store.Get(ctx, id)
	if sourceErr != nil {
		return Result{}, sourceErr
	}
	s.event(op, id, "store_read", authoritative.Version)
	fillErr := s.fill(ctx, authoritative, gated)
	s.event(op, id, "fill", authoritative.Version)
	return Result{authoritative, "store", !errors.Is(err, ErrCacheMiss) || fillErr != nil}, nil
}

func (s Service) getStrategy(ctx context.Context, op uint64, id string) (Result, error) {
	switch s.Mode {
	case CacheAside:
		return s.readThrough(ctx, op, id, false)
	default: // Versioned
		return s.readThrough(ctx, op, id, !s.UngatedFills)
	}
}

func (s Service) putStrategy(ctx context.Context, op uint64, item catalog.Item) (Result, error) {
	authoritative, err := s.Store.Put(ctx, item)
	if err != nil {
		return Result{}, err
	}
	s.event(op, item.ID, "write_commit", authoritative.Version)
	var cacheErr error
	if s.Mode == CacheAside {
		var u UngatedCache
		if u, cacheErr = s.ungated(); cacheErr == nil {
			cacheErr = u.Delete(ctx, authoritative.ID)
		}
		s.event(op, item.ID, "delete", authoritative.Version)
	} else {
		cacheErr = s.fill(ctx, authoritative, !s.UngatedFills)
		s.event(op, item.ID, "fill", authoritative.Version)
	}
	// As with D, a cache failure never turns a committed source write into a failed write.
	return Result{authoritative, "store", cacheErr != nil}, nil
}

// Tracer records the per-key timeline S1 uses to show which interleaving made a read stale.
// Begin returns 0 for keys it does not trace; every other call is then skipped.
type Tracer interface {
	Begin(id, kind string) uint64
	Event(op uint64, id, name string, version int64)
}

func (s Service) begin(id, kind string) uint64 {
	if s.Trace == nil {
		return 0
	}
	return s.Trace.Begin(id, kind)
}

func (s Service) event(op uint64, id, name string, version int64) {
	if op != 0 {
		s.Trace.Event(op, id, name, version)
	}
}
