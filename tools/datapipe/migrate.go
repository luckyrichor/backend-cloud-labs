package datapipe

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Migration is one numbered up/down pair from deploy/mysql/migrations.
//
// MySQL DDL commits implicitly, so a migration is not atomic: a failure part-way leaves the
// earlier statements applied and the version unrecorded. The runner stops at the first error
// and reports the statement index; recovery is manual. bench_run is created by the P0 init
// script, not by these migrations, so rolling everything back never drops run registrations.
type Migration struct {
	Version  int
	Name     string
	Up, Down []string
	Checksum string // sha256 of the up file
}

var migrationFile = regexp.MustCompile(`^(\d{4})_([a-z0-9_]+)\.(up|down)\.sql$`)

func LoadMigrations(dir string) ([]Migration, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	byVersion := map[int]*Migration{}
	for _, e := range entries {
		m := migrationFile.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		v, _ := strconv.Atoi(m[1])
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		mig := byVersion[v]
		if mig == nil {
			mig = &Migration{Version: v, Name: m[2]}
			byVersion[v] = mig
		}
		if mig.Name != m[2] {
			return nil, fmt.Errorf("version %04d has two names: %s, %s", v, mig.Name, m[2])
		}
		if m[3] == "up" {
			sum := sha256.Sum256(raw)
			mig.Up, mig.Checksum = SplitSQL(string(raw)), hex.EncodeToString(sum[:])
		} else {
			mig.Down = SplitSQL(string(raw))
		}
	}
	var out []Migration
	for _, m := range byVersion {
		if len(m.Up) == 0 || len(m.Down) == 0 {
			return nil, fmt.Errorf("migration %04d_%s needs both up and down statements", m.Version, m.Name)
		}
		out = append(out, *m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	for i, m := range out {
		if m.Version != i+1 {
			return nil, fmt.Errorf("migration versions must be 1..n without gaps; found %04d at position %d", m.Version, i+1)
		}
	}
	return out, nil
}

// SplitSQL drops whole-line "--" comments and splits on a semicolon that ends a line.
// The migration files avoid semicolons inside literals, so no SQL lexer is needed.
func SplitSQL(text string) []string {
	var stmts []string
	var cur strings.Builder
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "--") {
			continue
		}
		cur.WriteString(line)
		cur.WriteByte('\n')
		if strings.HasSuffix(trimmed, ";") {
			stmts = append(stmts, strings.TrimSuffix(strings.TrimSpace(cur.String()), ";"))
			cur.Reset()
		}
	}
	if s := strings.TrimSpace(cur.String()); s != "" {
		stmts = append(stmts, s)
	}
	return stmts
}

const migrationTable = `CREATE TABLE IF NOT EXISTS schema_migrations (
  version INT NOT NULL PRIMARY KEY, name VARCHAR(128) NOT NULL,
  checksum CHAR(64) NOT NULL, applied_at DATETIME(6) NOT NULL)`

// Applied returns recorded versions and verifies that applied up files were not edited.
func Applied(ctx context.Context, db *sql.DB, all []Migration) (int, error) {
	if _, err := db.ExecContext(ctx, migrationTable); err != nil {
		return 0, fmt.Errorf("create schema_migrations: %w", err)
	}
	rows, err := db.QueryContext(ctx, `SELECT version, checksum FROM schema_migrations ORDER BY version`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	current := 0
	for rows.Next() {
		var v int
		var sum string
		if err := rows.Scan(&v, &sum); err != nil {
			return 0, err
		}
		if v != current+1 || v > len(all) {
			return 0, fmt.Errorf("schema_migrations has unexpected version %d after %d", v, current)
		}
		if all[v-1].Checksum != sum {
			return 0, fmt.Errorf("migration %04d_%s was edited after it was applied", v, all[v-1].Name)
		}
		current = v
	}
	return current, rows.Err()
}

func Up(ctx context.Context, db *sql.DB, all []Migration, to int) ([]int, error) {
	current, err := Applied(ctx, db, all)
	if err != nil {
		return nil, err
	}
	var done []int
	for _, m := range all[current:min(to, len(all))] {
		for i, stmt := range m.Up {
			if _, err := db.ExecContext(ctx, stmt); err != nil {
				return done, fmt.Errorf("%04d_%s up statement %d: %w", m.Version, m.Name, i+1, err)
			}
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO schema_migrations VALUES (?, ?, ?, NOW(6))`, m.Version, m.Name, m.Checksum); err != nil {
			return done, err
		}
		done = append(done, m.Version)
	}
	return done, nil
}

func Down(ctx context.Context, db *sql.DB, all []Migration, steps int) ([]int, error) {
	current, err := Applied(ctx, db, all)
	if err != nil {
		return nil, err
	}
	var done []int
	for v := current; v > 0 && len(done) < steps; v-- {
		m := all[v-1]
		for i, stmt := range m.Down {
			if _, err := db.ExecContext(ctx, stmt); err != nil {
				return done, fmt.Errorf("%04d_%s down statement %d: %w", m.Version, m.Name, i+1, err)
			}
		}
		if _, err := db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version = ?`, v); err != nil {
			return done, err
		}
		done = append(done, v)
	}
	return done, nil
}

var autoIncrement = regexp.MustCompile(` AUTO_INCREMENT=\d+`)

// SchemaFingerprint hashes SHOW CREATE TABLE for every table except bookkeeping ones, so
// an up → down → up round trip can be checked for an identical schema.
func SchemaFingerprint(ctx context.Context, db *sql.DB) (string, []string, error) {
	rows, err := db.QueryContext(ctx, `SELECT table_name FROM information_schema.tables WHERE table_schema = DATABASE() ORDER BY table_name`)
	if err != nil {
		return "", nil, err
	}
	tables := []string{}
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			rows.Close()
			return "", nil, err
		}
		if t != "schema_migrations" && t != "bench_run" && !strings.HasPrefix(t, "p0_") {
			tables = append(tables, t)
		}
	}
	rows.Close()
	h := sha256.New()
	for _, t := range tables {
		var name, ddl string
		if err := db.QueryRowContext(ctx, "SHOW CREATE TABLE `"+t+"`").Scan(&name, &ddl); err != nil {
			return "", nil, err
		}
		h.Write([]byte(autoIncrement.ReplaceAllString(ddl, "") + "\n"))
	}
	return hex.EncodeToString(h.Sum(nil)), tables, nil
}
