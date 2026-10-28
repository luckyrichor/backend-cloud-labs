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
	// Retain a version watermark across DEL/TTL in the S2 run-scoped namespace.
	PersistVersionFloor bool
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

var fencedFill = redis.NewScript(`
local incoming=ARGV[1]
local function newer(old) return old and (#old>#incoming or (#old==#incoming and old>incoming)) end
local floor=redis.call('GET',KEYS[2])
local old=redis.call('HGET',KEYS[1],'version')
if newer(floor) or newer(old) then return 0 end
redis.call('SET',KEYS[2],incoming)
redis.call('HSET',KEYS[1],'version',incoming,'item',ARGV[2])
redis.call('PEXPIRE',KEYS[1],ARGV[3])
return 1
`)

func (c *Redis) Get(ctx context.Context, id string) (catalog.Item, error) {
	raw, err := c.Client.HGet(ctx, c.Prefix+"v2:"+id, "item").Result()
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
	script := fill
	keys := []string{c.Prefix + "v2:" + item.ID}
	if c.PersistVersionFloor {
		script = fencedFill
		keys = append(keys, c.Prefix+"floor:"+item.ID)
	}
	result, err := script.Run(ctx, c.Client, keys, item.Version,
		string(raw), c.TTL.Milliseconds()).Int()
	return result == 1, err
}

// Set overwrites without the version gate: the plain SET of a textbook cache-aside fill.
func (c *Redis) Set(ctx context.Context, item catalog.Item) error {
	raw, err := json.Marshal(item)
	if err != nil {
		return err
	}
	key := c.Prefix + "v2:" + item.ID
	_, err = c.Client.TxPipelined(ctx, func(p redis.Pipeliner) error {
		p.HSet(ctx, key, "version", item.Version, "item", string(raw))
		p.PExpire(ctx, key, c.TTL)
		return nil
	})
	return err
}

func (c *Redis) Delete(ctx context.Context, id string) error {
	return c.Client.Del(ctx, c.Prefix+"v2:"+id).Err()
}
