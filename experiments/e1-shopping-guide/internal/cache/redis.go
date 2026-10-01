package cache

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/catalog"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/guide"
	"github.com/redis/go-redis/v9"
	"time"
)

type Redis struct {
	Client *redis.Client
	Prefix string
	TTL    time.Duration
}

// Compare decimal versions as strings to avoid Lua double's precision loss above 2^53.
var fill = redis.NewScript(`
local old = redis.call('HGET', KEYS[1], 'version')
local incoming = ARGV[1]
if old and (#old > #incoming or (#old == #incoming and old > incoming)) then return 0 end
redis.call('HSET', KEYS[1], 'version', incoming, 'item', ARGV[2])
redis.call('PEXPIRE', KEYS[1], ARGV[3])
return 1
`)

func (c *Redis) Get(ctx context.Context, id string) (catalog.Item, error) {
	raw, err := c.Client.HGet(ctx, c.Prefix+id, "item").Result()
	if errors.Is(err, redis.Nil) {
		return catalog.Item{}, guide.ErrCacheMiss
	}
	if err != nil {
		return catalog.Item{}, err
	}
	var item catalog.Item
	err = json.Unmarshal([]byte(raw), &item)
	return item, err
}
func (c *Redis) PutIfNewer(ctx context.Context, item catalog.Item) (bool, error) {
	raw, err := json.Marshal(item)
	if err != nil {
		return false, err
	}
	result, err := fill.Run(ctx, c.Client, []string{c.Prefix + item.ID}, item.Version,
		string(raw), c.TTL.Milliseconds()).Int()
	return result == 1, err
}
