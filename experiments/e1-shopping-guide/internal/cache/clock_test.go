package cache

import (
	"context"
	"errors"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/catalog"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/guide"
	"testing"
	"time"
)

func TestInjectedClockExpiryAndMonotonicFill(t *testing.T) {
	now := time.Unix(100, 0)
	c := NewMemoryWithClock(time.Minute, func() time.Time { return now })
	ctx := context.Background()
	_, _ = c.PutIfNewer(ctx, catalog.Item{ID: "sku", Version: 2})
	if ok, _ := c.PutIfNewer(ctx, catalog.Item{ID: "sku", Version: 1}); ok {
		t.Fatal("stale fill")
	}
	now = now.Add(time.Minute + time.Nanosecond)
	if _, err := c.Get(ctx, "sku"); !errors.Is(err, guide.ErrCacheMiss) {
		t.Fatal("not expired", err)
	}
}
