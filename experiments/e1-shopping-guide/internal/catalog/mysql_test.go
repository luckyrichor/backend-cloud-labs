package catalog_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/luckyrichor/backend-cloud-labs/support/telemetry/audit"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/catalog"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/catalogtest"
)

func mysqlStore(t *testing.T) *catalog.MySQLStore {
	t.Helper()
	if !catalogtest.UsingMySQL() {
		t.Skip("set E1_TEST_STORE=mysql and E1_MYSQL_TEST_DSN")
	}
	return catalogtest.NewStore(t).(*catalog.MySQLStore)
}

// Concurrent writers to one id must each get a distinct committed version, with no gaps:
// the version is bumped by the row-locking statement itself, not read-modify-written.
func TestMySQLConcurrentPutsGetDistinctVersions(t *testing.T) {
	store := mysqlStore(t)
	ctx := context.Background()
	const writers, each = 16, 25
	versions := make(chan int64, writers*each)
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < each; i++ {
				item, err := store.Put(ctx, catalog.Item{ID: "7", Title: fmt.Sprint(w), PriceCent: int64(i)})
				if err != nil {
					t.Error(err)
					return
				}
				versions <- item.Version
			}
		}(w)
	}
	wg.Wait()
	close(versions)
	var got []int
	for v := range versions {
		got = append(got, int(v))
	}
	sort.Ints(got)
	for i, v := range got {
		if v != i+1 {
			t.Fatalf("versions not 1..%d without gaps/duplicates: position %d holds %d", writers*each, i, v)
		}
	}
	final, err := store.Get(ctx, "7")
	if err != nil || final.Version != writers*each {
		t.Fatalf("final row version %d err %v", final.Version, err)
	}
}

func TestMySQLPutReturnsWhatAReadSees(t *testing.T) {
	store := mysqlStore(t)
	ctx := context.Background()
	written, err := store.Put(ctx, catalog.Item{ID: "42", Title: "kettle", PriceCent: 3990, Stock: 5})
	if err != nil {
		t.Fatal(err)
	}
	read, err := store.Get(ctx, "42")
	if err != nil {
		t.Fatal(err)
	}
	if read != written {
		t.Fatalf("read %+v differs from the returned commit %+v", read, written)
	}
	if read.UpdatedAt.Location() != time.UTC {
		t.Fatalf("updated_at not UTC: %v", read.UpdatedAt.Location())
	}
}

// Updates through the API must keep the imported category; versions above 2^53 stay exact.
func TestMySQLUpdateKeepsCategoryAndLargeVersions(t *testing.T) {
	store := mysqlStore(t)
	ctx := context.Background()
	if _, err := store.DB.Exec(`INSERT INTO items VALUES (5, 'imported', 373, 100, 1, 9007199254740993, NOW(6))`); err != nil {
		t.Fatal(err)
	}
	item, err := store.Put(ctx, catalog.Item{ID: "5", Title: "renamed", PriceCent: 90, Stock: 2})
	if err != nil {
		t.Fatal(err)
	}
	if item.Version != 9007199254740994 {
		t.Fatalf("version %d, want 9007199254740994", item.Version)
	}
	var category int
	if err := store.DB.QueryRow(`SELECT category_id FROM items WHERE id = 5`).Scan(&category); err != nil || category != 373 {
		t.Fatalf("category %d err %v", category, err)
	}
}

func TestMySQLRejectsNonCanonicalIDs(t *testing.T) {
	store := mysqlStore(t)
	ctx := context.Background()
	if _, err := store.Put(ctx, catalog.Item{ID: "7"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"007", "+7", "-1", "sku", "", "18446744073709551616"} {
		if _, err := store.Put(ctx, catalog.Item{ID: id}); !errors.Is(err, catalog.ErrInvalidID) {
			t.Errorf("Put(%q) err %v, want ErrInvalidID", id, err)
		}
		if _, err := store.Get(ctx, id); !errors.Is(err, catalog.ErrNotFound) {
			t.Errorf("Get(%q) err %v, want ErrNotFound", id, err)
		}
	}
}

func TestMySQLVersionOnlyMatchesCommittedRow(t *testing.T) {
	s := mysqlStore(t)
	ctx := context.Background()
	for _, id := range []string{"41", "42"} {
		written, err := s.Put(ctx, catalog.Item{ID: id, Title: "payload"})
		if err != nil {
			t.Fatal(err)
		}
		v, err := s.GetVersion(ctx, id)
		if err != nil || v != written.Version {
			t.Fatalf("version=%d want=%d err=%v", v, written.Version, err)
		}
	}
	if _, err := s.GetVersion(ctx, "999"); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatal(err)
	}
}

func TestMySQLCommitReturnAuditCoversAllWrittenKeys(t *testing.T) {
	s := mysqlStore(t)
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	w, err := audit.Open(path, "mysql-test")
	if err != nil {
		t.Fatal(err)
	}
	s.Audit = w
	start := audit.Now()
	for _, id := range []string{"1", "42", "1851455"} {
		if _, err := s.Put(context.Background(), catalog.Item{ID: id, Title: "row"}); err != nil {
			t.Fatal(err)
		}
	}
	end := audit.Now()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	seen := map[string]bool{}
	for sc.Scan() {
		var e audit.Event
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			t.Fatal("invalid audit")
		}
		if e.Event != "commit_return" {
			continue
		}
		if e.TNS < start || e.TNS > end || e.Version != 1 {
			t.Fatalf("bad commit event %+v", e)
		}
		seen[e.ID] = true
	}
	if len(seen) != 3 || sc.Err() != nil {
		t.Fatal("missing all-key commit events", sc.Err())
	}
}
