// itemload imports catalog/items.tsv into the empty items table and reconciles the result.
//
//	python3 scripts/with-p0-env.py go -C tools/datapipe run ./cmd/itemload \
//	    --catalog /home/ubuntu/datasets/backend-cloud-labs/p1/catalog --output ../../.local/p1/itemload.json
//
// The file hash must match catalog/manifest.json before anything is written; afterwards
// COUNT, MAX(id), SUM(price_cent) and SUM(stock) must equal the manifest's figures.
// Rows go in as multi-row INSERTs inside transactions; server settings are left unchanged
// (no LOAD DATA LOCAL, which would need local_infile switched on).
package main

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	dp "github.com/luckyrichor/backend-cloud-labs/tools/datapipe"
)

type manifest struct {
	DataVersion string        `json:"data_version"`
	Artifacts   []dp.Artifact `json:"artifacts"`
	Stats       struct {
		Items struct {
			SumPrice int64 `json:"sum_price_cent"`
			SumStock int64 `json:"sum_stock"`
		} `json:"items"`
	} `json:"stats"`
}

func main() {
	dir := flag.String("catalog", "", "generated catalog directory")
	output := flag.String("output", "", "result JSON path")
	batch := flag.Int("batch", 2000, "rows per INSERT")
	perTx := flag.Int("batches-per-tx", 5, "INSERT statements per transaction")
	flag.Parse()
	if *dir == "" || *output == "" {
		fatal(errors.New("--catalog and --output required"))
	}
	raw, err := os.ReadFile(filepath.Join(*dir, "manifest.json"))
	if err != nil {
		fatal(err)
	}
	var m manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		fatal(err)
	}
	var want dp.Artifact
	for _, a := range m.Artifacts {
		if a.Path == "items.tsv" {
			want = a
		}
	}
	itemsPath := filepath.Join(*dir, "items.tsv")
	got, err := dp.HashFile(itemsPath)
	if err != nil {
		fatal(err)
	}
	if want.SHA256 == "" || got.SHA256 != want.SHA256 {
		fatal(errors.New("items.tsv hash does not match manifest"))
	}
	db, err := open()
	if err != nil {
		fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	var existing int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM items`).Scan(&existing); err != nil {
		fatal(fmt.Errorf("items table not readable (run dbmigrate up first): %w", err))
	}
	if existing != 0 {
		fatal(fmt.Errorf("items already holds %d rows; refusing to mix imports", existing))
	}

	start := time.Now()
	f, err := os.Open(itemsPath)
	if err != nil {
		fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<16), 1<<20)
	var rows int64
	var args []any
	var tx *sql.Tx
	statements := 0
	flush := func() error {
		if len(args) == 0 {
			return nil
		}
		n := len(args) / 7
		q := "INSERT INTO items (id,title,category_id,price_cent,stock,version,updated_at) VALUES " +
			strings.TrimSuffix(strings.Repeat("(?,?,?,?,?,?,?),", n), ",")
		if tx == nil {
			var err error
			if tx, err = db.BeginTx(ctx, nil); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, q, args...); err != nil {
			tx.Rollback()
			return err
		}
		args = args[:0]
		statements++
		if statements%*perTx == 0 {
			err := tx.Commit()
			tx = nil
			return err
		}
		return nil
	}
	for sc.Scan() {
		cols := strings.Split(sc.Text(), "\t")
		if len(cols) != 7 {
			fatal(fmt.Errorf("line %d has %d columns", rows+1, len(cols)))
		}
		for _, c := range cols {
			args = append(args, c)
		}
		rows++
		if len(args) == 7**batch {
			if err := flush(); err != nil {
				fatal(fmt.Errorf("insert near row %d: %w", rows, err))
			}
		}
	}
	if err := sc.Err(); err != nil {
		fatal(err)
	}
	if err := flush(); err != nil {
		fatal(err)
	}
	if tx != nil {
		if err := tx.Commit(); err != nil {
			fatal(err)
		}
	}
	elapsed := time.Since(start)

	var count, maxID, sumPrice, sumStock int64
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*), MAX(id), SUM(price_cent), SUM(stock) FROM items`).Scan(&count, &maxID, &sumPrice, &sumStock); err != nil {
		fatal(err)
	}
	checks := map[string]bool{
		"count":     count == want.Rows && count == rows,
		"max_id":    maxID == want.Rows,
		"sum_price": sumPrice == m.Stats.Items.SumPrice,
		"sum_stock": sumStock == m.Stats.Items.SumStock,
	}
	ok := true
	for _, v := range checks {
		ok = ok && v
	}
	result := map[string]any{
		"data_version": m.DataVersion, "items_sha256": got.SHA256, "rows_read": rows,
		"table":  map[string]int64{"count": count, "max_id": maxID, "sum_price_cent": sumPrice, "sum_stock": sumStock},
		"checks": checks, "ok": ok, "seconds": elapsed.Seconds(), "batch": *batch, "batches_per_tx": *perTx,
		"rows_per_second": strconv.FormatFloat(float64(rows)/elapsed.Seconds(), 'f', 0, 64),
		"note":            "import throughput is a one-off load figure, not an E1 performance result",
	}
	if err := os.MkdirAll(filepath.Dir(*output), 0o755); err != nil {
		fatal(err)
	}
	if err := dp.WriteJSON(*output, result); err != nil {
		fatal(err)
	}
	b, _ := json.Marshal(result)
	fmt.Println(string(b))
	if !ok {
		os.Exit(1)
	}
}

func open() (*sql.DB, error) {
	cfg, err := mysql.ParseDSN(os.Getenv("LOADGEN_MYSQL_DSN"))
	if err != nil || cfg.DBName == "" {
		return nil, errors.New("LOADGEN_MYSQL_DSN missing or without database name")
	}
	cfg.MultiStatements = false
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return nil, errors.New("database configuration rejected")
	}
	if err := db.Ping(); err != nil {
		return nil, errors.New("database connection failed")
	}
	return db, nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "itemload:", err)
	os.Exit(1)
}
