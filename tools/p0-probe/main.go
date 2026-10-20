// P0 real MySQL authentication and ROW-event compatibility probe.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/go-mysql-org/go-mysql/canal"
	_ "github.com/go-sql-driver/mysql"
	"io"
	"log/slog"
	"os"
	"time"
)

type handler struct {
	canal.DummyEventHandler
	events chan bool
	id     int64
}

func (h *handler) OnRow(e *canal.RowsEvent) error {
	if e.Table.Schema == "bcl" && e.Table.Name == "p0_binlog" && e.Action == canal.InsertAction {
		for _, row := range e.Rows {
			if fmt.Sprint(row[0]) == fmt.Sprint(h.id) {
				select {
				case h.events <- true:
				default:
				}
			}
		}
	}
	return nil
}
func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	db, err := sql.Open("mysql", os.Getenv("LOADGEN_MYSQL_DSN"))
	if err != nil {
		return fmt.Errorf("SQL configuration failed")
	}
	defer db.Close()
	var version, format, image string
	if db.QueryRowContext(ctx, "SELECT VERSION(),@@binlog_format,@@binlog_row_image").Scan(&version, &format, &image) != nil {
		return fmt.Errorf("SQL connection failed")
	}
	cfg := canal.NewDefaultConfig()
	cfg.Addr = os.Getenv("P0_MYSQL_ADDRESS")
	cfg.User = os.Getenv("P0_MYSQL_USER")
	cfg.Password = os.Getenv("P0_MYSQL_PASSWORD")
	cfg.Dump.ExecutionPath = ""
	cfg.IncludeTableRegex = []string{"^bcl\\.p0_binlog$"}
	cfg.ServerID = 271820
	cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	c, err := canal.NewCanal(cfg)
	if err != nil {
		return fmt.Errorf("canal connection failed")
	}
	defer c.Close()
	pos, err := c.GetMasterPos()
	if err != nil {
		return fmt.Errorf("master position failed")
	}
	h := &handler{events: make(chan bool, 1), id: time.Now().UnixNano()}
	c.SetEventHandler(h)
	done := make(chan error, 1)
	go func() { done <- c.RunFrom(pos) }()
	if _, err = db.ExecContext(ctx, "INSERT INTO p0_binlog(id,version) VALUES(?,1)", h.id); err != nil {
		return fmt.Errorf("test insert failed")
	}
	select {
	case <-h.events:
	case <-ctx.Done():
		return fmt.Errorf("ROW event deadline exceeded")
	case <-done:
		return fmt.Errorf("binlog reader stopped")
	}
	if format != "ROW" || image != "FULL" {
		return fmt.Errorf("unexpected binlog configuration")
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"mysql_version": version, "binlog_format": format, "binlog_row_image": image, "canal_row_event_received": true, "scope": "one real INSERT; not crash recovery or S2 correctness"})
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
