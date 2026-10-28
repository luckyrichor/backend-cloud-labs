// Package binlogbridge publishes committed ROW changes and their resume position
// in one Redis Lua operation. Failure before the durable position is replayable.
package binlogbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/go-mysql-org/go-mysql/canal"
	"github.com/go-mysql-org/go-mysql/mysql"
	"github.com/go-mysql-org/go-mysql/replication"
	"github.com/luckyrichor/backend-cloud-labs/support/telemetry/audit"
	"github.com/redis/go-redis/v9"
)

type Change struct {
	ID       string
	Version  int64
	Observed int64
}
type Handler struct {
	canal.DummyEventHandler
	Redis                        *redis.Client
	Database, Stream, Checkpoint string
	Audit                        *audit.Writer
	Pending                      []Change
}

var publish = redis.NewScript(`
local st=redis.call('TYPE',KEYS[1]).ok
local cp=redis.call('TYPE',KEYS[2]).ok
if (st~='none' and st~='stream') or (cp~='none' and cp~='string') then return redis.error_reply('bridge key type conflict') end
for i=2,#ARGV,4 do
 redis.call('XADD',KEYS[1],'*','id',ARGV[i],'version',ARGV[i+1],'observed_ns',ARGV[i+2],'published_ns',ARGV[i+3])
end
redis.call('SET',KEYS[2],ARGV[1])
return (#ARGV-1)/4
`)

func (h *Handler) OnRow(e *canal.RowsEvent) error {
	if e.Table.Schema != h.Database || e.Table.Name != "items" {
		return nil
	}
	if e.Action == canal.DeleteAction {
		return errors.New("source DELETE unsupported; bridge stopped without advancing checkpoint")
	}
	idIndex, versionIndex := -1, -1
	for i, c := range e.Table.Columns {
		if c.Name == "id" {
			idIndex = i
		}
		if c.Name == "version" {
			versionIndex = i
		}
	}
	if idIndex < 0 || versionIndex < 0 {
		return errors.New("ROW schema lacks id/version")
	}
	for i, row := range e.Rows {
		if e.Action == canal.UpdateAction && i%2 == 0 {
			continue
		} // FULL before/after pairs
		if idIndex >= len(row) || versionIndex >= len(row) {
			return errors.New("ROW image incomplete")
		}
		id := fmt.Sprint(row[idIndex])
		if _, err := strconv.ParseUint(id, 10, 64); err != nil {
			return errors.New("ROW id invalid")
		}
		version, err := strconv.ParseInt(fmt.Sprint(row[versionIndex]), 10, 64)
		if err != nil || version < 1 {
			return errors.New("ROW version invalid")
		}
		h.Pending = append(h.Pending, Change{ID: id, Version: version, Observed: audit.Now()})
	}
	return nil
}
func (h *Handler) OnPosSynced(_ *replication.EventHeader, pos mysql.Position, _ mysql.GTIDSet, _ bool) error {
	// canal only invokes this at XID/rotation/DDL boundaries, never midway through ROW events.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	raw, _ := json.Marshal(pos)
	args := []any{string(raw)}
	published := audit.Now()
	for _, change := range h.Pending {
		args = append(args, change.ID, change.Version, change.Observed, published)
	}
	if _, err := publish.Run(ctx, h.Redis, []string{h.Stream, h.Checkpoint}, args...).Result(); err != nil {
		return errors.New("atomic stream/checkpoint publish failed")
	}
	if h.Audit != nil {
		for _, change := range h.Pending {
			h.Audit.RecordAt(ctx, change.ID, "stream_published", 0, change.Version, published)
		}
	}
	h.Pending = nil
	return nil
}
