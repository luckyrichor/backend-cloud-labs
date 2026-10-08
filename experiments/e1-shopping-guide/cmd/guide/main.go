package main

import (
	"context"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/cache"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/catalog"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/guide"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/httpapi"
	"github.com/redis/go-redis/v9"
	"log"
	"net/http"
	"os"
	"time"
)

func main() {
	store := catalog.NewMemoryStore()
	_, _ = store.Put(context.Background(), catalog.Item{ID: "kettle", Title: "水壶", PriceCent: 4990, Stock: 10})
	var cached guide.Cache = cache.NewMemory(time.Minute)
	if address := os.Getenv("REDIS_ADDR"); address != "" {
		client := redis.NewClient(&redis.Options{Addr: address, DialTimeout: 200 * time.Millisecond,
			ReadTimeout: 200 * time.Millisecond, WriteTimeout: 200 * time.Millisecond, MaxRetries: -1})
		defer client.Close()
		cached = &cache.Redis{Client: client, Prefix: "guide:", TTL: time.Minute}
	}
	mode := guide.ReadMode(os.Getenv("CACHE_READ_MODE"))
	if !guide.ValidReadMode(mode) {
		log.Fatal("CACHE_READ_MODE must be strict or cache-first")
	}
	server := &http.Server{Addr: "127.0.0.1:8086", Handler: httpapi.Handler(guide.Service{Store: store, Cache: cached, Mode: mode}),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second}
	log.Fatal(server.ListenAndServe())
}
