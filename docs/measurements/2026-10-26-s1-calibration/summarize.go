package main

import (
	"encoding/json"
	"fmt"
	hdr "github.com/HdrHistogram/hdrhistogram-go"
	"os"
	"path/filepath"
)

func main() {
	rows := []map[string]any{}
	files, _ := filepath.Glob(filepath.Join(os.Args[1], "read-only-*.json"))
	for _, p := range files {
		var d struct {
			Config struct{ Rate int }
			Result struct {
				Valid    bool
				Observed float64 `json:"observed_window_send_rate"`
				Dropped  int
				Reads    struct {
					Count, Success, Failed int
					Latency                map[string]any `json:"latency_us"`
				}
				Server struct {
					Latency struct{ Get *hdr.Snapshot }
					QPS     float64 `json:"source_reads_per_second"`
					Hit     float64 `json:"cache_hit_ratio"`
				}
			}
		}
		b, e := os.ReadFile(p)
		if e != nil {
			panic(e)
		}
		if e = json.Unmarshal(b, &d); e != nil {
			panic(e)
		}
		h := hdr.Import(d.Result.Server.Latency.Get)
		rows = append(rows, map[string]any{"rate": d.Config.Rate, "valid": d.Result.Valid, "reads": d.Result.Reads.Count, "success": d.Result.Reads.Success, "failed": d.Result.Reads.Failed, "dropped": d.Result.Dropped, "observed_rate": d.Result.Observed, "client_latency_us": d.Result.Reads.Latency, "server_latency_us": map[string]any{"count": h.TotalCount(), "p50": h.ValueAtQuantile(50), "p95": h.ValueAtQuantile(95), "p99": h.ValueAtQuantile(99)}, "source_reads_per_second": d.Result.Server.QPS, "cache_hit_ratio": d.Result.Server.Hit})
	}
	b, _ := json.MarshalIndent(rows, "", "  ")
	fmt.Println(string(b))
}
