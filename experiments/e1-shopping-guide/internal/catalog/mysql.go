package catalog

import (
	"context"
	"database/sql"
	"errors"
	"github.com/luckyrichor/backend-cloud-labs/support/telemetry/audit"
	"strconv"
	"time"
)

// ErrInvalidID rejects ids the MySQL source cannot hold (items.id is BIGINT UNSIGNED).
var ErrInvalidID = errors.New("catalog: item id must be a canonical unsigned decimal")

// MySQLStore is the source of truth for S1/S2, backed by the items table of
// deploy/mysql/migrations/0001. It holds no cache of its own.
//
// Put is one autocommitted INSERT … ON DUPLICATE KEY UPDATE: the row lock taken by
// that statement serialises concurrent writers to one id, and the version is bumped
// inside it. LAST_INSERT_ID(expr) hands the committed version back in the OK packet,
// so no second round trip (and no window for another writer) is needed to learn it.
type MySQLStore struct {
	DB    *sql.DB
	Audit *audit.Writer
	now   func() time.Time
}

func NewMySQLStore(db *sql.DB) *MySQLStore { return &MySQLStore{DB: db, now: time.Now} }

// NewMySQLStoreWithClock injects the clock that stamps updated_at, as MemoryStore does.
func NewMySQLStoreWithClock(db *sql.DB, now func() time.Time) *MySQLStore {
	return &MySQLStore{DB: db, now: now}
}

// parseID accepts only the canonical form, so "007" and "7" can never be two names for one row.
func parseID(id string) (uint64, bool) {
	n, err := strconv.ParseUint(id, 10, 64)
	return n, err == nil && strconv.FormatUint(n, 10) == id
}

// Get returns ErrNotFound for ids that cannot exist in the table, matching a missing row.
func (s *MySQLStore) Get(ctx context.Context, id string) (Item, error) {
	n, ok := parseID(id)
	if !ok {
		if err := ctx.Err(); err != nil {
			return Item{}, err
		}
		return Item{}, ErrNotFound
	}
	item := Item{ID: id}
	err := s.DB.QueryRowContext(ctx,
		`SELECT title, price_cent, stock, version, updated_at FROM items WHERE id = ?`, n).
		Scan(&item.Title, &item.PriceCent, &item.Stock, &item.Version, &item.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Item{}, ErrNotFound
	}
	if err != nil {
		return Item{}, err
	}
	item.UpdatedAt = item.UpdatedAt.UTC()
	return item, nil
}

// Put creates or updates the row and returns the committed copy. The caller's Version and
// UpdatedAt are ignored; category_id is kept on update and 0 on create (the HTTP API has
// no category field).
func (s *MySQLStore) Put(ctx context.Context, item Item) (Item, error) {
	n, ok := parseID(item.ID)
	if !ok {
		return Item{}, ErrInvalidID
	}
	if err := ctx.Err(); err != nil {
		return Item{}, err
	}
	// updated_at is DATETIME(6): stamp at microsecond precision so the returned copy equals a later read.
	now := s.now().UTC().Truncate(time.Microsecond)
	res, err := s.DB.ExecContext(ctx, `INSERT INTO items (id, title, category_id, price_cent, stock, version, updated_at)
VALUES (?, ?, 0, ?, ?, LAST_INSERT_ID(1), ?) AS incoming
ON DUPLICATE KEY UPDATE title = incoming.title, price_cent = incoming.price_cent, stock = incoming.stock,
  version = LAST_INSERT_ID(items.version + 1), updated_at = incoming.updated_at`,
		n, item.Title, item.PriceCent, item.Stock, now)
	committedAt := audit.Now() // immediately after autocommit ExecContext returns
	if err != nil {
		return Item{}, err
	}
	version, err := res.LastInsertId()
	if err != nil {
		return Item{}, err
	}
	if version <= 0 {
		return Item{}, errors.New("catalog: MySQL did not report the committed version")
	}
	item.Version, item.UpdatedAt = version, now
	if s.Audit != nil {
		s.Audit.RecordAt(ctx, item.ID, "commit_return", 0, version, committedAt)
	}
	return item, nil
}

// GetVersion performs one primary-key lookup, without fetching/decode of the item payload.
func (s *MySQLStore) GetVersion(ctx context.Context, id string) (int64, error) {
	n, ok := parseID(id)
	if !ok {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		return 0, ErrNotFound
	}
	var version int64
	err := s.DB.QueryRowContext(ctx, "SELECT version FROM items WHERE id = ?", n).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return version, err
}
