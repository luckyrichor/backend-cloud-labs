// dbmigrate applies deploy/mysql/migrations to the database named in LOADGEN_MYSQL_DSN.
//
//	python3 scripts/with-p0-env.py go -C tools/datapipe run ./cmd/dbmigrate --dir ../../deploy/mysql/migrations status|up|down|roundtrip
//
// roundtrip proves reversibility: it requires every managed table to be empty, then runs
// down-all, up-all and compares the schema fingerprint with the starting one.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/go-sql-driver/mysql"
	dp "github.com/luckyrichor/backend-cloud-labs/tools/datapipe"
)

func main() {
	dir := flag.String("dir", "deploy/mysql/migrations", "migration directory")
	to := flag.Int("to", 1<<30, "up: highest version to apply")
	steps := flag.Int("steps", 1, "down: number of versions to revert")
	flag.Parse()
	if flag.NArg() != 1 {
		fatal(errors.New("command: status | up | down | roundtrip"))
	}
	all, err := dp.LoadMigrations(*dir)
	if err != nil {
		fatal(err)
	}
	db, err := open()
	if err != nil {
		fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	report := map[string]any{"command": flag.Arg(0), "migrations": len(all)}
	switch flag.Arg(0) {
	case "status":
		current, err := dp.Applied(ctx, db, all)
		if err != nil {
			fatal(err)
		}
		fp, tables, err := dp.SchemaFingerprint(ctx, db)
		if err != nil {
			fatal(err)
		}
		report["applied"], report["schema_fingerprint"], report["tables"] = current, fp, tables
	case "up":
		done, err := dp.Up(ctx, db, all, *to)
		report["applied_now"] = done
		if err != nil {
			emit(report)
			fatal(err)
		}
	case "down":
		done, err := dp.Down(ctx, db, all, *steps)
		report["reverted_now"] = done
		if err != nil {
			emit(report)
			fatal(err)
		}
	case "roundtrip":
		if err := roundtrip(ctx, db, all, report); err != nil {
			emit(report)
			fatal(err)
		}
	default:
		fatal(fmt.Errorf("unknown command %q", flag.Arg(0)))
	}
	emit(report)
}

func roundtrip(ctx context.Context, db *sql.DB, all []dp.Migration, report map[string]any) error {
	current, err := dp.Applied(ctx, db, all)
	if err != nil {
		return err
	}
	if current != len(all) {
		return fmt.Errorf("roundtrip needs all %d migrations applied, have %d", len(all), current)
	}
	before, tables, err := dp.SchemaFingerprint(ctx, db)
	if err != nil {
		return err
	}
	for _, t := range tables {
		var any int
		err := db.QueryRowContext(ctx, "SELECT 1 FROM `"+t+"` LIMIT 1").Scan(&any)
		if err == nil {
			return fmt.Errorf("table %s has rows; roundtrip would destroy data", t)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	reverted, err := dp.Down(ctx, db, all, len(all))
	report["reverted"] = reverted
	if err != nil {
		return err
	}
	_, empty, err := dp.SchemaFingerprint(ctx, db)
	if err != nil {
		return err
	}
	report["tables_after_down"] = empty
	if len(empty) != 0 {
		return fmt.Errorf("tables left after full down: %v", empty)
	}
	applied, err := dp.Up(ctx, db, all, len(all))
	report["reapplied"] = applied
	if err != nil {
		return err
	}
	after, _, err := dp.SchemaFingerprint(ctx, db)
	if err != nil {
		return err
	}
	report["fingerprint_before"], report["fingerprint_after"], report["tables"] = before, after, tables
	if before != after {
		return errors.New("schema differs after down/up round trip")
	}
	report["roundtrip"] = "identical"
	return nil
}

// open never echoes the DSN or a driver error that could contain it.
func open() (*sql.DB, error) {
	cfg, err := mysql.ParseDSN(os.Getenv("LOADGEN_MYSQL_DSN"))
	if err != nil || cfg.DBName == "" {
		return nil, errors.New("LOADGEN_MYSQL_DSN missing or without database name")
	}
	cfg.MultiStatements, cfg.ParseTime = false, true
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return nil, errors.New("database configuration rejected")
	}
	if err := db.Ping(); err != nil {
		return nil, errors.New("database connection failed")
	}
	return db, nil
}

func emit(v any) {
	b, _ := json.MarshalIndent(v, "", " ")
	fmt.Println(string(b))
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "dbmigrate:", err)
	os.Exit(1)
}
