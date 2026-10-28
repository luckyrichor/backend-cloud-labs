package coherence

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/guide"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/httpapi"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/cache"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/catalog"
	"github.com/redis/go-redis/v9"
)

func TestDeletingWriterCachesCannotInvalidatePeerL1(t *testing.T) {
	ctx := context.Background()
	shared := cache.NewMemory(time.Hour)
	a := &Cache{Local: cache.NewMemory(time.Hour), Shared: shared}
	defer a.Close()
	b := &Cache{Local: cache.NewMemory(time.Hour), Shared: shared}
	defer b.Close()
	first := catalog.Item{ID: "7", Version: 1}
	a.Set(ctx, first)
	b.Get(ctx, "7")
	if err := a.Delete(ctx, "7"); err != nil {
		t.Fatal(err)
	}
	old, err := b.Get(ctx, "7")
	if err != nil || old.Version != 1 {
		t.Fatal("baseline peer cache should remain stale")
	}
	b.PutIfNewer(ctx, catalog.Item{ID: "7", Version: 3})
	b.PutIfNewer(ctx, catalog.Item{ID: "7", Version: 2})
	now, _ := b.Get(ctx, "7")
	if now.Version != 3 {
		t.Fatal("late fill regressed L1")
	}
}
func TestConsumerRestartRecoversPendingAndReorderedEvents(t *testing.T) {
	addr := os.Getenv("E1_REDIS_TEST_ADDR")
	if addr == "" {
		t.Skip("requires isolated test Redis")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rc := redis.NewClient(&redis.Options{Addr: addr})
	defer rc.Close()
	stream := fmt.Sprintf("test:s2:%d", time.Now().UnixNano())
	defer rc.Del(context.Background(), stream)
	source := catalog.NewMemoryStore()
	source.Put(ctx, catalog.Item{ID: "7"})
	source.Put(ctx, catalog.Item{ID: "7"})
	rc.XGroupCreateMkStream(ctx, stream, "instance-b", "0")
	rc.XAdd(ctx, &redis.XAddArgs{Stream: stream, Values: map[string]any{"id": "7", "version": "2"}})
	// First consumer died after delivery but before applying/ACKing.
	delivered, err := rc.XReadGroup(ctx, &redis.XReadGroupArgs{Group: "instance-b", Consumer: "instance-b", Streams: []string{stream, ">"}, Count: 1}).Result()
	if err != nil || len(delivered) == 0 {
		t.Fatal(err)
	}
	tier := &Cache{Local: cache.NewMemory(time.Minute), Shared: cache.NewMemory(time.Minute)}
	defer tier.Close()
	restarted := &Consumer{Redis: rc, Stream: stream, Group: "instance-b", Name: "instance-b", Store: source, Cache: tier}
	done := make(chan error, 1)
	go func() { done <- restarted.Run(ctx) }()
	deadline := time.Now().Add(3 * time.Second)
	for restarted.Applied.Load() < 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if restarted.Applied.Load() != 1 || restarted.Replayed.Load() != 1 {
		t.Fatal("PEL was not recovered")
	}
	pending, err := rc.XPending(ctx, stream, "instance-b").Result()
	if err != nil || pending.Count != 0 {
		t.Fatal("ACK missing", err)
	}
	source.Put(ctx, catalog.Item{ID: "7"})
	for _, v := range []string{"3", "2", "3", "1"} {
		rc.XAdd(ctx, &redis.XAddArgs{Stream: stream, Values: map[string]any{"id": "7", "version": v}})
	}
	for restarted.Applied.Load() < 5 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	result, err := tier.Get(ctx, "7")
	if err != nil || result.Version != 3 || restarted.Applied.Load() != 5 {
		t.Fatal("duplicate/reorder failed", result, err)
	}
	cancel()
	<-done
}

// Exercise the HTTP adapter too: it creates a repair queue when absent, which
// must not silently reintroduce private prefixes into an explicitly shared L2.
func TestSharedNamespaceSurvivesHTTPAdapterAndEventApply(t *testing.T) {
	ctx := context.Background()
	source := catalog.NewMemoryStore()
	source.Put(ctx, catalog.Item{ID: "7"})
	shared := cache.NewMemory(time.Minute)
	a := &Cache{Local: cache.NewMemory(time.Minute), Shared: shared}
	defer a.Close()
	b := &Cache{Local: cache.NewMemory(time.Minute), Shared: shared}
	defer b.Close()
	service := guide.NewService(source, b, guide.CacheAside)
	service.SharedNamespace = true
	service.GatedAside = true
	handler := httpapi.Handler(service)
	read := func() int64 {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", "/items/7", nil))
		var result guide.Result
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil {
			t.Fatal(w.Body.String())
		}
		return result.Item.Version
	}
	if read() != 1 {
		t.Fatal("prime failed")
	}
	source.Put(ctx, catalog.Item{ID: "7"})
	a.Delete(ctx, "7")
	if read() != 1 {
		t.Fatal("baseline peer should remain stale")
	}
	authoritative, _ := source.Get(ctx, "7")
	b.PutIfNewer(ctx, authoritative)
	if read() != 2 {
		t.Fatal("HTTP reader cannot see event-applied shared namespace")
	}
}
