// Package catalogtest lets the existing E1 tests run against either source.
//
// By default NewStore returns a MemoryStore, so `go test ./...` needs nothing external.
// With E1_TEST_STORE=mysql and E1_MYSQL_TEST_DSN (a user allowed to create databases),
// every test process gets its own throwaway database built from the real migration file,
// and each NewStore call starts from an empty items table. Go runs test packages as
// parallel processes, so per-process databases keep them from seeing each other's rows.
package catalogtest

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/catalog"
)

var (
	once   sync.Once
	shared *sql.DB
	setup  error
)

// UsingMySQL reports whether this test process exercises the MySQL source.
func UsingMySQL() bool { return os.Getenv("E1_TEST_STORE") == "mysql" }

// NewStore returns an empty store of the selected kind.
func NewStore(t testing.TB) catalog.Store {
	return NewStoreWithClock(t, time.Now)
}

func NewStoreWithClock(t testing.TB, now func() time.Time) catalog.Store {
	t.Helper()
	if !UsingMySQL() {
		return catalog.NewMemoryStoreWithClock(now)
	}
	db := database(t)
	if _, err := db.Exec(`TRUNCATE TABLE items`); err != nil {
		t.Fatalf("truncate items: %v", err)
	}
	return catalog.NewMySQLStoreWithClock(db, now)
}

func database(t testing.TB) *sql.DB {
	t.Helper()
	once.Do(func() { shared, setup = create() })
	if setup != nil {
		t.Fatalf("MySQL test database: %v", setup)
	}
	return shared
}

func create() (*sql.DB, error) {
	cfg, err := mysql.ParseDSN(os.Getenv("E1_MYSQL_TEST_DSN"))
	if err != nil {
		return nil, err
	}
	var token [6]byte
	rand.Read(token[:])
	name := "e1test_" + hex.EncodeToString(token[:])
	admin, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return nil, err
	}
	defer admin.Close()
	if _, err := admin.Exec("CREATE DATABASE " + name); err != nil {
		return nil, err
	}
	cfg.DBName = name
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return nil, err
	}
	_, self, _, _ := runtime.Caller(0)
	up, err := os.ReadFile(filepath.Join(filepath.Dir(self), "../../../../deploy/mysql/migrations/0001_e1_items.up.sql"))
	if err != nil {
		return nil, err
	}
	var stmt []string
	for _, line := range strings.Split(string(up), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "--") {
			stmt = append(stmt, line)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := db.ExecContext(ctx, strings.TrimSuffix(strings.TrimSpace(strings.Join(stmt, "\n")), ";")); err != nil {
		return nil, err
	}
	// Throwaway databases live in a throwaway server (scripts/check.sh); nothing to drop here.
	return db, nil
}
