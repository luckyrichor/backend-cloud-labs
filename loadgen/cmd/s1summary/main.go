// s1summary derives scalar metrics from unmodified S1 JSON/HdrHistogram results.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	hdr "github.com/HdrHistogram/hdrhistogram-go"
	lg "github.com/luckyrichor/backend-cloud-labs/loadgen"
)

type document struct {
	Story       string `json:"story"`
	Arm         string `json:"arm"`
	Repeat      int    `json:"repeat"`
	Commit      string `json:"git_commit"`
	DataVersion string `json:"data_version"`
	Config      struct {
		Rate   int     `json:"rate"`
		Ratio  float64 `json:"read_ratio"`
		Bypass bool    `json:"bypass_writes"`
	} `json:"config"`
	Result lg.S1Result `json:"result"`
}

func quantiles(s *hdr.Snapshot) (map[string]int64, error) {
	if s == nil || len(s.Counts) == 0 {
		return nil, fmt.Errorf("server histogram missing")
	}
	h := hdr.Import(s)
	return map[string]int64{"count": h.TotalCount(), "p50_us": h.ValueAtQuantile(50), "p95_us": h.ValueAtQuantile(95), "p99_us": h.ValueAtQuantile(99), "max_us": h.Max()}, nil
}
func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: s1summary <results-directory>")
		os.Exit(2)
	}
	rows := []map[string]any{}
	files, err := filepath.Glob(filepath.Join(os.Args[1], "*.json"))
	if err != nil {
		panic(err)
	}
	sort.Strings(files)
	for _, p := range files {
		if strings.Contains(filepath.Base(p), ".done.") || strings.Contains(filepath.Base(p), ".network.") || strings.Contains(filepath.Base(p), ".worker-error.") || strings.Contains(filepath.Base(p), ".container.") || strings.Contains(filepath.Base(p), ".correctness.") {
			continue
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			panic(err)
		}
		var d document
		if err = json.Unmarshal(raw, &d); err != nil {
			panic(err)
		}
		if d.Story == "" {
			continue
		}
		b, err := json.Marshal(d.Result.Server["latency"])
		if err != nil {
			panic(err)
		}
		var snapshots struct{ Get, Put *hdr.Snapshot }
		if err = json.Unmarshal(b, &snapshots); err != nil {
			panic(err)
		}
		get, err := quantiles(snapshots.Get)
		if err != nil && d.Result.Valid {
			panic(err)
		}
		put, err := quantiles(snapshots.Put)
		if err != nil && d.Result.Valid {
			panic(err)
		}
		rows = append(rows, map[string]any{"file": filepath.Base(p), "story": d.Story, "arm": d.Arm, "repeat": d.Repeat,
			"source_commit": d.Commit, "data_version": d.DataVersion, "rate": d.Config.Rate, "ratio": d.Config.Ratio,
			"bypass": d.Config.Bypass, "valid": d.Result.Valid, "invalid_reasons": d.Result.InvalidReasons,
			"reads": d.Result.Reads.Count, "writes": d.Result.Writes.Count, "read_failed": d.Result.Reads.Failed, "write_failed": d.Result.Writes.Failed,
			"client_get_us": d.Result.Reads.Latency, "client_put_us": d.Result.Writes.Latency,
			"server_get_us": get, "server_put_us": put, "stale_reads": d.Result.StaleReads, "stale_ratio": d.Result.StaleRatio,
			"cache_hit_ratio": d.Result.Server["cache_hit_ratio"], "source_reads_per_second": d.Result.Server["source_reads_per_second"],
			"server_counters": d.Result.Server["counters_delta"], "stale_age_us": d.Result.StaleAge})
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err = enc.Encode(rows); err != nil {
		panic(err)
	}
}
