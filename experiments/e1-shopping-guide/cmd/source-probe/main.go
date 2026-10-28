// source-probe is a serial, read-only calibration, never a formal strategy run.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	hdr "github.com/HdrHistogram/hdrhistogram-go"
	_ "github.com/go-sql-driver/mysql"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/catalog"
	"github.com/luckyrichor/backend-cloud-labs/support/telemetry/audit"
	"log"
	"os"
	"strconv"
)

func main() {
	n := flag.Int("count", 1000, "serial read samples")
	flag.Parse()
	if *n < 1000 || *n > 100000 {
		log.Fatal("count must be 1000..100000")
	}
	db, err := sql.Open("mysql", os.Getenv("GUIDE_MYSQL_DSN"))
	if err != nil {
		log.Fatal("SQL configuration failed")
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	store := catalog.NewMySQLStore(db)
	ctx := context.Background()
	if _, err := store.Get(ctx, "1"); err != nil {
		log.Fatal("source probe connection failed")
	}
	hist := hdr.New(1, 600000000, 3)
	values := make([]int64, 0, *n)
	for i := 0; i < *n; i++ {
		t := audit.Now()
		_, err := store.Get(ctx, strconv.Itoa(1+(i*7919)%2000000))
		us := max(1, (audit.Now()-t)/1000)
		if err != nil {
			log.Fatal("source probe read failed")
		}
		if hist.RecordValue(us) != nil {
			log.Fatal("histogram overflow")
		}
		values = append(values, us)
	}
	json.NewEncoder(os.Stdout).Encode(map[string]any{"scope": "serial read-only source.Get calibration", "count": *n, "unit": "us", "p50": hist.ValueAtQuantile(50), "p99": hist.ValueAtQuantile(99), "max": hist.Max(), "raw_us": values, "histogram": hist.Export(), "clock": "linux-CLOCK_MONOTONIC"})
}
