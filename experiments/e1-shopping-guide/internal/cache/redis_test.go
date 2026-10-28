package cache_test

import (
	"context"
	"errors"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/cache"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/catalog"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/catalogtest"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/guide"
	"github.com/redis/go-redis/v9"
	"os"
	"testing"
	"time"
)

func TestRedisAtomicFill(t *testing.T) {
	address := os.Getenv("E1_REDIS_TEST_ADDR")
	if address == "" {
		t.Skip("set E1_REDIS_TEST_ADDR for Redis integration")
	}
	client := redis.NewClient(&redis.Options{Addr: address})
	defer client.Close()
	ctx := context.Background()
	key := "e1-test:" + t.Name() + ":"
	defer client.Del(ctx, key+"v2:sku")
	c := cache.Redis{Client: client, Prefix: key, TTL: time.Minute}
	if _, err := c.PutIfNewer(ctx, catalog.Item{ID: "sku", Version: 9007199254740993}); err != nil {
		t.Fatal(err)
	}
	if ok, err := c.PutIfNewer(ctx, catalog.Item{ID: "sku", Version: 9007199254740992}); err != nil || ok {
		t.Fatal("accepted old", err)
	}
	got, err := c.Get(ctx, "sku")
	if err != nil || got.Version != 9007199254740993 {
		t.Fatal(got, err)
	}
}

func TestRedisLegacyPayloadIsNotUsedAfterJSONContractChange(t *testing.T) {
	address := os.Getenv("E1_REDIS_TEST_ADDR")
	if address == "" {
		t.Skip("set E1_REDIS_TEST_ADDR")
	}
	client := redis.NewClient(&redis.Options{Addr: address})
	defer client.Close()
	ctx := context.Background()
	prefix := "e1-test:legacy:"
	defer client.Del(ctx, prefix+"sku", prefix+"v2:sku")
	if err := client.HSet(ctx, prefix+"sku", "version", "1", "item", `{"ID":"sku","PriceCent":999,"Version":1}`).Err(); err != nil {
		t.Fatal(err)
	}
	c := cache.Redis{Client: client, Prefix: prefix, TTL: time.Minute}
	if _, err := c.Get(ctx, "sku"); !errors.Is(err, guide.ErrCacheMiss) {
		t.Fatal("legacy hit", err)
	}
	_, err := c.PutIfNewer(ctx, catalog.Item{ID: "sku", PriceCent: 100, Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	item, err := c.Get(ctx, "sku")
	if err != nil || item.PriceCent != 100 {
		t.Fatal(item, err)
	}
}

func TestRedisNewSourceLifetimeUsesIndependentNamespace(t *testing.T) {
	address := os.Getenv("E1_REDIS_TEST_ADDR")
	if address == "" {
		t.Skip("set E1_REDIS_TEST_ADDR")
	}
	client := redis.NewClient(&redis.Options{Addr: address})
	defer client.Close()
	c := &cache.Redis{Client: client, Prefix: "e1-test:lifetime:", TTL: time.Minute}
	ctx := context.Background()
	old := guide.NewService(catalogtest.NewStore(t), c, guide.CacheFirst)
	for i := 0; i < 5; i++ {
		if _, err := old.Put(ctx, catalog.Item{ID: "101", PriceCent: 900}); err != nil {
			t.Fatal(err)
		}
	}
	fresh := guide.NewService(catalogtest.NewStore(t), c, guide.CacheFirst)
	if _, err := fresh.Put(ctx, catalog.Item{ID: "101", PriceCent: 100}); err != nil {
		t.Fatal(err)
	}
	r, err := fresh.Get(ctx, "101")
	if err != nil || r.Item.Version != 1 || r.Item.PriceCent != 100 || r.Source != "cache_unvalidated" {
		t.Fatal(r, err)
	}
}

// Set is deliberately ungated (cache-aside fills and the reverse-validation arm): it must
// overwrite a newer version, and Delete must leave a miss.
func TestRedisSetOverwritesAndDeleteMisses(t *testing.T) {
	address := os.Getenv("E1_REDIS_TEST_ADDR")
	if address == "" {
		t.Skip("set E1_REDIS_TEST_ADDR")
	}
	client := redis.NewClient(&redis.Options{Addr: address})
	defer client.Close()
	ctx := context.Background()
	key := "e1-test:" + t.Name() + ":"
	defer client.Del(ctx, key+"v2:sku")
	c := cache.Redis{Client: client, Prefix: key, TTL: time.Minute}
	if _, err := c.PutIfNewer(ctx, catalog.Item{ID: "sku", Version: 5}); err != nil {
		t.Fatal(err)
	}
	if err := c.Set(ctx, catalog.Item{ID: "sku", Version: 3}); err != nil {
		t.Fatal(err)
	}
	if got, err := c.Get(ctx, "sku"); err != nil || got.Version != 3 {
		t.Fatal("Set must overwrite regardless of version", got, err)
	}
	if ttl := client.PTTL(ctx, key+"v2:sku").Val(); ttl <= 0 || ttl > time.Minute {
		t.Fatal("Set must apply the TTL", ttl)
	}
	// The gate still compares against what Set wrote.
	if ok, _ := c.PutIfNewer(ctx, catalog.Item{ID: "sku", Version: 2}); ok {
		t.Fatal("gated fill accepted an older version after Set")
	}
	if err := c.Delete(ctx, "sku"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get(ctx, "sku"); !errors.Is(err, guide.ErrCacheMiss) {
		t.Fatal("Delete left the entry", err)
	}
}

func TestRedisS2WatermarkSurvivesDeleteAndCacheExpiry(t *testing.T) {
	addr := os.Getenv("E1_REDIS_TEST_ADDR")
	if addr == "" {
		t.Skip("requires isolated Redis")
	}
	ctx := context.Background()
	rc := redis.NewClient(&redis.Options{Addr: addr})
	defer rc.Close()
	prefix := "test:fenced:" + t.Name() + ":"
	defer rc.Del(ctx, prefix+"v2:7", prefix+"floor:7")
	c := &cache.Redis{Client: rc, Prefix: prefix, TTL: time.Millisecond, PersistVersionFloor: true}
	if ok, err := c.PutIfNewer(ctx, catalog.Item{ID: "7", Version: 9007199254740993}); err != nil || !ok {
		t.Fatal(err)
	}
	c.Delete(ctx, "7")
	if ok, err := c.PutIfNewer(ctx, catalog.Item{ID: "7", Version: 9007199254740992}); err != nil || ok {
		t.Fatal("DEL removed version fence", err)
	}
	c.PutIfNewer(ctx, catalog.Item{ID: "7", Version: 9007199254740993})
	time.Sleep(3 * time.Millisecond)
	if ok, err := c.PutIfNewer(ctx, catalog.Item{ID: "7", Version: 1}); err != nil || ok {
		t.Fatal("TTL removed version fence", err)
	}
	if _, err := c.Get(ctx, "7"); !errors.Is(err, guide.ErrCacheMiss) {
		t.Fatal("stale payload was refilled", err)
	}
}
