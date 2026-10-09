package cache

import (
	"context"
	"fmt"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/catalog"
	"testing"
	"time"
)

func TestExpiredOldNamespaceEntriesAreReclaimedOnContinuedFills(t *testing.T) {
	now := time.Unix(100, 0)
	c := NewMemoryWithClock(time.Minute, func() time.Time { return now })
	ctx := context.Background()
	_, _ = c.PutIfNewer(ctx, catalog.Item{ID: "old-epoch:sku", Version: 1})
	now = now.Add(2 * time.Minute)
	for i := 0; i < 256; i++ {
		_, _ = c.PutIfNewer(ctx, catalog.Item{ID: fmt.Sprintf("new-epoch:%d", i), Version: 1})
	}
	if _, exists := c.entries["old-epoch:sku"]; exists {
		t.Fatal("expired retired namespace never reclaimed")
	}
	if len(c.entries) != 256 {
		t.Fatal("removed live generation", len(c.entries))
	}
}
