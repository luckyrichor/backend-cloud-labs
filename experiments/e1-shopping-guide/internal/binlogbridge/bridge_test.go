package binlogbridge

import (
	"context"
	"fmt"
	"github.com/go-mysql-org/go-mysql/mysql"
	"github.com/redis/go-redis/v9"
	"os"
	"testing"
	"time"
)

func TestPublishFailureNeverAdvancesCheckpointOrDropsPending(t *testing.T) {
	addr := os.Getenv("E1_REDIS_TEST_ADDR")
	if addr == "" {
		t.Skip("requires isolated test Redis")
	}
	ctx := context.Background()
	rc := redis.NewClient(&redis.Options{Addr: addr})
	defer rc.Close()
	key := fmt.Sprintf("test:bridge:%d", time.Now().UnixNano())
	stream, checkpoint := key+":events", key+":checkpoint"
	defer rc.Del(ctx, stream, checkpoint)
	rc.Set(ctx, checkpoint, "old-position", 0)
	rc.Set(ctx, stream, "wrong-type", 0)
	h := &Handler{Redis: rc, Stream: stream, Checkpoint: checkpoint, Pending: []Change{{ID: "7", Version: 9007199254740993, Observed: 1}, {ID: "8", Version: 2, Observed: 2}}}
	if err := h.OnPosSynced(nil, mysql.Position{Name: "binlog.000001", Pos: 100}, nil, false); err == nil {
		t.Fatal("wrong stream type must fail")
	}
	value, _ := rc.Get(ctx, checkpoint).Result()
	if value != "old-position" || len(h.Pending) != 2 {
		t.Fatal("failed transaction lost resume point")
	}
	rc.Del(ctx, stream)
	if err := h.OnPosSynced(nil, mysql.Position{Name: "binlog.000001", Pos: 100}, nil, false); err != nil {
		t.Fatal(err)
	}
	events, err := rc.XRange(ctx, stream, "-", "+").Result()
	if err != nil || len(events) != 2 || len(h.Pending) != 0 {
		t.Fatal("committed publish incomplete", err)
	}
	if events[0].Values["version"] != "9007199254740993" {
		t.Fatal("version lost integer precision")
	}
	value, _ = rc.Get(ctx, checkpoint).Result()
	if value == "old-position" {
		t.Fatal("position not persisted")
	}
}
