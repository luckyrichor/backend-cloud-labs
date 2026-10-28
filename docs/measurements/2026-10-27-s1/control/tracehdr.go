// tracehdr derives diagnostic HdrHistograms from C's existing same-process trace.
// Scope: one hot key, whole attempt including priming/warmup. Not the formal GET window.
package main

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"fmt"
	hdr "github.com/HdrHistogram/hdrhistogram-go"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type event struct {
	Op    uint64 `json:"op"`
	Kind  string `json:"kind"`
	Event string `json:"event"`
	T     int64  `json:"t_ns"`
}
type operation struct {
	read               bool
	start, source, end int64
}
type document struct {
	Story  string `json:"story"`
	Arm    string `json:"arm"`
	Repeat int    `json:"repeat"`
	Commit string `json:"git_commit"`
	Config struct {
		Rate  int     `json:"rate"`
		Ratio float64 `json:"read_ratio"`
	} `json:"config"`
	Result struct {
		Valid bool `json:"valid"`
	} `json:"result"`
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
func data(path string) []byte {
	b, err := os.ReadFile(path)
	must(err)
	if strings.HasSuffix(path, ".gz") {
		r, err := gzip.NewReader(strings.NewReader(string(b)))
		must(err)
		b, err = io.ReadAll(r)
		must(err)
		must(r.Close())
	}
	return b
}
func summary(h *hdr.Histogram) map[string]any {
	return map[string]any{"count": h.TotalCount(), "min_us": h.Min(), "p50_us": h.ValueAtQuantile(50), "p95_us": h.ValueAtQuantile(95), "p99_us": h.ValueAtQuantile(99), "max_us": h.Max(), "histogram": h.Export()}
}
func main() {
	if len(os.Args) < 2 {
		panic("usage: tracehdr <completed-results-dir> [...]")
	}
	rows := []map[string]any{}
	for _, dir := range os.Args[1:] {
		paths, err := filepath.Glob(filepath.Join(dir, "*.trace.jsonl.gz"))
		must(err)
		sort.Strings(paths)
		for _, path := range paths {
			stem := strings.TrimSuffix(filepath.Base(path), ".trace.jsonl.gz")
			raw := filepath.Join(dir, stem+".json")
			if _, err := os.Stat(raw); err != nil {
				raw += ".gz"
			}
			var d document
			must(json.Unmarshal(data(raw), &d))
			if d.Arm != "C" || !d.Result.Valid || (d.Story != "S1" && d.Story != "S1-tx-calibration") {
				continue
			}
			if d.Commit != "d2ca677db32a17eaed9aff776244262b88d42801" {
				panic("unexpected measured source")
			}
			operations := map[uint64]*operation{}
			boundaries := 0
			scanner := bufio.NewScanner(strings.NewReader(string(data(path))))
			for scanner.Scan() {
				var e event
				must(json.Unmarshal(scanner.Bytes(), &e))
				if e.Op == 1 && e.Event == "begin" {
					boundaries++
				}
				o := operations[e.Op]
				if o == nil {
					o = &operation{}
					operations[e.Op] = o
				}
				switch e.Event {
				case "begin":
					o.read = e.Kind == "read"
				case "read_start":
					o.start = e.T
				case "store_read":
					o.source = e.T
				case "cache_hit", "cache_validated", "fill":
					o.end = e.T
				}
			}
			must(scanner.Err())
			if boundaries != 1 {
				panic("mixed/missing trace instance boundary")
			}
			source, pathDuration, residual := hdr.New(1, 600000000, 3), hdr.New(1, 600000000, 3), hdr.New(1, 600000000, 3)
			incomplete := 0
			for _, o := range operations {
				if !o.read {
					continue
				}
				if o.start <= 0 || o.source < o.start || o.end < o.source {
					incomplete++
					continue
				}
				must(source.RecordValue(max(1, (o.source-o.start)/1000)))
				must(pathDuration.RecordValue(max(1, (o.end-o.start)/1000)))
				must(residual.RecordValue(max(1, (o.end-o.source)/1000)))
			}
			if incomplete != 0 {
				panic("incomplete strict trace read intervals")
			}
			if source.TotalCount() == 0 {
				panic("no complete strict read intervals")
			}
			rows = append(rows, map[string]any{"file": filepath.Base(dir) + "/" + filepath.Base(path), "story": d.Story, "rate": d.Config.Rate, "ratio": d.Config.Ratio, "repeat": d.Repeat, "source_commit": d.Commit, "incomplete_reads": incomplete, "source_get_observation_us": summary(source), "core_read_path_us": summary(pathDuration), "after_source_us": summary(residual)})
		}
	}
	out := map[string]any{"scope": "C hot-key whole-attempt diagnostic, including priming/warmup; not formal-window latency. Source interval includes connection-pool wait, driver, MySQL point lookup and trace overhead. Core path ends at cache hit/fill, before HTTP JSON serialization. Quantiles use HdrHistogram(1us,600s,3 digits).", "rows": rows}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	must(enc.Encode(out))
	fmt.Fprint(os.Stderr, "trace groups: ", len(rows), "\n")
}
