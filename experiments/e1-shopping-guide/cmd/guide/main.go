package main

import (
	"context"
	"database/sql"
	"errors"
	"github.com/go-sql-driver/mysql"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/cache"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/catalog"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/guide"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/httpapi"
	"github.com/luckyrichor/backend-cloud-labs/support/telemetry"
	"github.com/redis/go-redis/v9"
	"log"
	"net/http"
	"os"
	"time"
)

func main() {
	adminAddress := telemetry.Address("ADMIN_ADDRESS", "127.0.0.1:9086")
	telemetry.ExitHealthcheck(adminAddress)
	metrics := &telemetry.Metrics{}
	admin, err := telemetry.Start(adminAddress, metrics)
	if err != nil {
		log.Fatal(err)
	}
	defer admin.Close()
	writeToken := os.Getenv("GUIDE_WRITE_TOKEN")
	var store catalog.Store
	switch os.Getenv("GUIDE_SOURCE") {
	case "", "memory":
		memory := catalog.NewMemoryStore()
		_, _ = memory.Put(context.Background(), catalog.Item{ID: "kettle", Title: "水壶", PriceCent: 4990, Stock: 10})
		store = memory
	case "mysql":
		// A shared MySQL source must not accept anonymous writes.
		if writeToken == "" {
			log.Fatal("GUIDE_SOURCE=mysql requires GUIDE_WRITE_TOKEN")
		}
		db, err := openMySQL(os.Getenv("GUIDE_MYSQL_DSN"))
		if err != nil {
			log.Fatal(err)
		}
		defer db.Close()
		store = catalog.NewMySQLStore(db)
	default:
		log.Fatal("GUIDE_SOURCE must be memory or mysql")
	}
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
	server := &http.Server{Addr: telemetry.Address("GUIDE_ADDRESS", "127.0.0.1:8086"), Handler: metrics.HTTP(httpapi.HandlerWithOptions(guide.Service{Store: store, Cache: cached, Mode: mode}, httpapi.Options{WriteToken: writeToken})),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second}
	log.Fatal(server.ListenAndServe())
}

// openMySQL never echoes the DSN or a driver error that could contain it.
func openMySQL(dsn string) (*sql.DB, error) {
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil || cfg.DBName == "" {
		return nil, errors.New("GUIDE_MYSQL_DSN missing or without database name")
	}
	cfg.ParseTime, cfg.Loc = true, time.UTC
	cfg.Timeout, cfg.ReadTimeout, cfg.WriteTimeout = 2*time.Second, 2*time.Second, 2*time.Second
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return nil, errors.New("MySQL configuration rejected")
	}
	db.SetMaxOpenConns(64)
	db.SetMaxIdleConns(64)
	db.SetConnMaxLifetime(30 * time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, errors.New("MySQL connection failed")
	}
	return db, nil
}
