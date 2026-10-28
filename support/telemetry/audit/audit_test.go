package audit_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/luckyrichor/backend-cloud-labs/support/telemetry/audit"
)

func TestAllKeysConcurrentCorrelatedAndExclusive(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("host clock audit is Linux-only")
	}
	path := filepath.Join(t.TempDir(), "events.jsonl")
	w, err := audit.Open(path, "instance-a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := audit.Open(path, "instance-a"); err == nil {
		t.Fatal("existing evidence overwritten")
	}
	before := audit.Now()
	var wg sync.WaitGroup
	for i := range 100 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprint(i)
			ctx := audit.WithLabels(context.Background(), "run1", "window", id)
			op := w.Begin(ctx, id)
			w.RecordAt(ctx, id, "commit_return", 0, 2, audit.Now())
			w.End(ctx, id, op, 2, nil)
		}(i)
	}
	wg.Wait()
	after := audit.Now()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	ids := map[string]int{}
	ops := map[uint64]string{}
	count := 0
	boot := ""
	for s.Scan() {
		var e audit.Event
		if err := json.Unmarshal(s.Bytes(), &e); err != nil {
			t.Fatal(err)
		}
		if boot == "" {
			boot = e.Boot
		}
		if e.Boot != boot || boot == "" || e.Instance != "instance-a" {
			t.Fatal("clock/instance identity missing")
		}
		if e.Event == "start" {
			continue
		}
		count++
		if e.TNS < before || e.TNS > after || e.Run != "run1" || e.Phase != "window" || e.Request != e.ID {
			t.Fatalf("invalid event %+v", e)
		}
		ids[e.ID]++
		if e.Event == "read_begin" {
			ops[e.Op] = e.ID
		}
		if e.Event == "read_return" && ops[e.Op] != e.ID {
			t.Fatal("read correlation lost")
		}
	}
	if s.Err() != nil || count != 300 || len(ids) != 100 {
		t.Fatalf("count=%d keys=%d error=%v", count, len(ids), s.Err())
	}
	w.Begin(context.Background(), "late")
	if _, failed := w.Status(); !failed {
		t.Fatal("missing write-after-close failure")
	}
}
