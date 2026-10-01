package cache_test

import (
	"context"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/cache"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/catalog"
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
	defer client.Del(ctx, key+"sku")
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
