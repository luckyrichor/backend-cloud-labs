package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/cache"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/catalog"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/guide"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/httpapi"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/observe"
	"github.com/luckyrichor/backend-cloud-labs/support/telemetry"
	"github.com/redis/go-redis/v9"
)

// Environment (all optional unless noted):
//
//	GUIDE_ADDRESS, ADMIN_ADDRESS      listeners (defaults 127.0.0.1:8086 / :9086)
//	GUIDE_SOURCE=memory|mysql         source of truth (default memory)
//	GUIDE_MYSQL_DSN                   required for mysql
//	GUIDE_WRITE_TOKEN                 bearer token for PUT; required for mysql
//	REDIS_ADDR                        use Redis instead of the in-process cache
//	CACHE_READ_MODE                   strict (default) | cache-first | cache-aside | versioned
//	GUIDE_CACHE_TTL                   cache entry lifetime (default 1m)
//	GUIDE_UNGATED_FILLS=1             versioned arm fills with plain SET (S1 reverse validation)
//	GUIDE_TRACE_ID, GUIDE_TRACE_FILE  append the event timeline of one key as JSON lines
func main() {
	// Enabled only in dedicated profiling runs, excluded from performance comparisons.
	if os.Getenv("GUIDE_PROFILE_MUTEX") == "1" {
		runtime.SetMutexProfileFraction(1)
	}
	adminAddress := telemetry.Address("ADMIN_ADDRESS", "127.0.0.1:9086")
	telemetry.ExitHealthcheck(adminAddress)
	metrics := &telemetry.Metrics{}

	ttl := time.Minute
	if text := os.Getenv("GUIDE_CACHE_TTL"); text != "" {
		d, err := time.ParseDuration(text)
		if err != nil || d <= 0 {
			log.Fatal("GUIDE_CACHE_TTL must be a positive duration")
		}
		ttl = d
	}
	mode := guide.ReadMode(os.Getenv("CACHE_READ_MODE"))
	if !guide.ValidReadMode(mode) {
		log.Fatal("CACHE_READ_MODE must be strict, cache-first, cache-aside or versioned")
	}
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

	var cached guide.Cache = cache.NewMemory(ttl)
	if address := os.Getenv("REDIS_ADDR"); address != "" {
		client := redis.NewClient(&redis.Options{Addr: address, DialTimeout: 200 * time.Millisecond,
			ReadTimeout: 200 * time.Millisecond, WriteTimeout: 200 * time.Millisecond, MaxRetries: -1, PoolSize: 64})
		defer client.Close()
		cached = &cache.Redis{Client: client, Prefix: "guide:", TTL: ttl}
	}

	service := guide.NewService(observe.NewStore(store, metrics.Counter), observe.NewCache(cached, metrics.Counter), mode)
	service.UngatedFills = os.Getenv("GUIDE_UNGATED_FILLS") == "1"
	if service.UngatedFills && mode != guide.Versioned {
		log.Fatal("GUIDE_UNGATED_FILLS only applies to CACHE_READ_MODE=versioned")
	}
	var tracer *observe.FileTracer
	if key := os.Getenv("GUIDE_TRACE_ID"); key != "" {
		var err error
		if tracer, err = observe.NewFileTracer(key, os.Getenv("GUIDE_TRACE_FILE")); err != nil {
			log.Fatal("GUIDE_TRACE_FILE: ", err)
		}
		service.Trace = tracer
		go func() {
			for range time.Tick(200 * time.Millisecond) {
				_ = tracer.Flush()
			}
		}()
	}
	latency := observe.NewLatency()
	metrics.Route("GET /debug/latency", latency.Handler())
	strategy := []byte(`{"mode":"` + string(service.Mode) + `","ungated_fills":` + map[bool]string{true: "true", false: "false"}[service.UngatedFills] + `,"ttl":"` + ttl.String() + `"}`)
	metrics.Route("GET /debug/strategy", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(strategy)
	}))
	admin, err := telemetry.Start(adminAddress, metrics)
	if err != nil {
		log.Fatal(err)
	}
	defer admin.Close()

	handler := httpapi.HandlerWithOptions(service, httpapi.Options{WriteToken: writeToken})
	server := &http.Server{Addr: telemetry.Address("GUIDE_ADDRESS", "127.0.0.1:8086"), Handler: metrics.HTTP(latency.Wrap(handler)),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-stop
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}()
	if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		log.Print(err)
	}
	if tracer != nil {
		_ = tracer.Close()
	}
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
