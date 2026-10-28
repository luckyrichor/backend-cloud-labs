package coherence

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/catalog"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/guide"
	"github.com/luckyrichor/backend-cloud-labs/support/telemetry/audit"
	"github.com/redis/go-redis/v9"
)

type Consumer struct {
	Redis                     *redis.Client
	Stream, Group, Name       string
	Store                     catalog.Store
	Cache                     guide.Cache
	Audit                     *audit.Writer
	Paused                    atomic.Bool
	Applied, Errors, Replayed atomic.Uint64
}

func (c *Consumer) apply(ctx context.Context, m redis.XMessage) error {
	id, ok := m.Values["id"].(string)
	if !ok || id == "" {
		return errors.New("stream item lacks id")
	}
	version, err := strconv.ParseInt(fmt.Sprint(m.Values["version"]), 10, 64)
	if err != nil || version < 1 {
		return errors.New("invalid stream version")
	}
	// Fetch current primary state; an old/replayed event must never reintroduce an old payload.
	item, err := c.Store.Get(ctx, id)
	if err != nil {
		return err
	}
	if item.Version < version {
		return errors.New("source behind committed event")
	}
	if _, err = c.Cache.PutIfNewer(ctx, item); err != nil {
		return err
	}
	if c.Audit != nil {
		c.Audit.RecordAt(ctx, id, "stream_applied", 0, item.Version, audit.Now())
	}
	if err = c.Redis.XAck(ctx, c.Stream, c.Group, m.ID).Err(); err != nil {
		return err
	}
	c.Applied.Add(1)
	return nil
}
func (c *Consumer) Run(ctx context.Context) error {
	// Stable group AND consumer name recover this instance's PEL after a crash.
	if err := c.Redis.XGroupCreateMkStream(ctx, c.Stream, c.Group, "0").Err(); err != nil && err.Error() != "BUSYGROUP Consumer Group name already exists" {
		return errors.New("stream group setup failed")
	}
	for ctx.Err() == nil {
		if c.Paused.Load() {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(20 * time.Millisecond):
			}
			continue
		}
		pending, err := c.Redis.XReadGroup(ctx, &redis.XReadGroupArgs{Group: c.Group, Consumer: c.Name, Streams: []string{c.Stream, "0"}, Count: 32}).Result()
		handled := false
		if err == nil {
			for _, stream := range pending {
				for _, m := range stream.Messages {
					handled = true
					c.Replayed.Add(1)
					if c.apply(ctx, m) != nil {
						c.Errors.Add(1)
						select {
						case <-ctx.Done():
							return ctx.Err()
						case <-time.After(50 * time.Millisecond):
						}
					}
				}
			}
		} else if !errors.Is(err, redis.Nil) {
			c.Errors.Add(1)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(50 * time.Millisecond):
			}
			continue
		}
		if handled {
			continue
		}
		streams, err := c.Redis.XReadGroup(ctx, &redis.XReadGroupArgs{Group: c.Group, Consumer: c.Name, Streams: []string{c.Stream, ">"}, Count: 32, Block: 100 * time.Millisecond}).Result()
		if err != nil {
			if !errors.Is(err, redis.Nil) && ctx.Err() == nil {
				c.Errors.Add(1)
			}
			continue
		}
		for _, stream := range streams {
			for _, m := range stream.Messages {
				if c.apply(ctx, m) != nil {
					c.Errors.Add(1)
				}
			}
		}
	}
	return ctx.Err()
}
