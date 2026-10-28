package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"regexp"
	"syscall"
	"time"

	"github.com/go-mysql-org/go-mysql/canal"
	"github.com/go-mysql-org/go-mysql/mysql"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/binlogbridge"
	"github.com/luckyrichor/backend-cloud-labs/support/telemetry/audit"
	"github.com/redis/go-redis/v9"
)

func run() error {
	database := flag.String("database", "bcl_s1", "dedicated experiment schema")
	stream := flag.String("stream", "bcl:s2:events", "dedicated stream")
	checkpoint := flag.String("checkpoint", "bcl:s2:checkpoint", "durable binlog resume position")
	initialize := flag.Bool("initialize", false, "explicitly initialize an empty stream at current primary position")
	serverID := flag.Uint("server-id", 271821, "unique replication server ID")
	auditFile := flag.String("audit-file", "", "exclusive all-key event log")
	flag.Parse()
	if !regexp.MustCompile(`^[a-zA-Z0-9_]+$`).MatchString(*database) {
		return errors.New("invalid experiment schema")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	rc := redis.NewClient(&redis.Options{Addr: os.Getenv("REDIS_ADDR"), DialTimeout: time.Second, ReadTimeout: 2 * time.Second, WriteTimeout: 2 * time.Second})
	defer rc.Close()
	cfg := canal.NewDefaultConfig()
	cfg.Addr = os.Getenv("P0_MYSQL_ADDRESS")
	cfg.User = os.Getenv("P0_MYSQL_USER")
	cfg.Password = os.Getenv("P0_MYSQL_PASSWORD")
	cfg.ServerID = uint32(*serverID)
	cfg.Dump.ExecutionPath = ""
	cfg.IncludeTableRegex = []string{"^" + regexp.QuoteMeta(*database) + "\\.items$"}
	cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	c, err := canal.NewCanal(cfg)
	if err != nil {
		return errors.New("binlog connection setup failed")
	}
	defer c.Close()
	var w *audit.Writer
	if *auditFile != "" {
		w, err = audit.Open(*auditFile, "binlog-bridge")
		if err != nil {
			return errors.New("bridge audit setup failed")
		}
		defer w.Close()
	}
	raw, err := rc.Get(ctx, *checkpoint).Result()
	if errors.Is(err, redis.Nil) {
		if !*initialize {
			return errors.New("checkpoint missing; explicit initialization of empty stream required")
		}
		count, err := rc.XLen(ctx, *stream).Result()
		if err != nil || count != 0 {
			return errors.New("refuse initialization over existing stream")
		}
		pos, err := c.GetMasterPos()
		if err != nil {
			return errors.New("initial binlog position unavailable")
		}
		b, _ := json.Marshal(pos)
		created, err := rc.SetNX(ctx, *checkpoint, string(b), 0).Result()
		if err != nil || !created {
			return errors.New("checkpoint initialization raced")
		}
		raw = string(b)
	} else if err != nil {
		return errors.New("checkpoint read failed")
	}
	var pos mysql.Position
	if json.Unmarshal([]byte(raw), &pos) != nil || pos.Name == "" || pos.Pos < 4 {
		return errors.New("invalid resume position")
	}
	h := &binlogbridge.Handler{Redis: rc, Database: *database, Stream: *stream, Checkpoint: *checkpoint, Audit: w}
	c.SetEventHandler(h)
	done := make(chan error, 1)
	go func() { done <- c.RunFrom(pos) }()
	select {
	case <-ctx.Done():
		c.Close()
		<-done
		return nil
	case <-done:
		return errors.New("binlog stopped; checkpoint retained, restart required")
	}
}
func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}
