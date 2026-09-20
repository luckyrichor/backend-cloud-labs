// Package catalog holds the item model and the source-of-truth store that the
// shopping-guide service reads through.
//
// The cache sits in front of this package, never inside it: a store that
// silently caches its own reads makes the consistency problem invisible, which
// is the one thing this experiment exists to make visible.
package catalog

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrNotFound is returned when an item id is absent from the store.
var ErrNotFound = errors.New("catalog: item not found")

// Item is a product as the guide surface needs it.
//
// Version is what makes stale reads detectable: a cached copy carrying a lower
// version than the store's is stale by definition, with no clock involved.
type Item struct {
	ID        string
	Title     string
	PriceCent int64
	Stock     int
	Version   int64
	UpdatedAt time.Time
}

// Store is the source of truth. Implementations must be safe for concurrent use.
type Store interface {
	Get(ctx context.Context, id string) (Item, error)
	Put(ctx context.Context, item Item) (Item, error)
}

// MemoryStore is an in-process Store for tests and local runs.
//
// It assigns versions itself, so no caller can forget to bump one.
type MemoryStore struct {
	mu    sync.RWMutex
	items map[string]Item
	now   func() time.Time
}

// NewMemoryStore returns an empty store using wall-clock time.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{items: make(map[string]Item), now: time.Now}
}

// NewMemoryStoreWithClock returns a store with an injected clock, so tests do
// not have to sleep to observe ordering.
func NewMemoryStoreWithClock(now func() time.Time) *MemoryStore {
	return &MemoryStore{items: make(map[string]Item), now: now}
}

// Get returns the current item, or ErrNotFound.
func (s *MemoryStore) Get(ctx context.Context, id string) (Item, error) {
	if err := ctx.Err(); err != nil {
		return Item{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.items[id]
	if !ok {
		return Item{}, ErrNotFound
	}
	return item, nil
}

// Put writes the item and returns the stored copy with its new version.
// The caller's Version and UpdatedAt fields are ignored.
func (s *MemoryStore) Put(ctx context.Context, item Item) (Item, error) {
	if err := ctx.Err(); err != nil {
		return Item{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	item.Version = s.items[item.ID].Version + 1
	item.UpdatedAt = s.now()
	s.items[item.ID] = item
	return item, nil
}
