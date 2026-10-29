package main

import (
	"compress/gzip"
	"encoding/json"
	hdr "github.com/HdrHistogram/hdrhistogram-go"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
)

type Obs struct{ Lower, Upper, Write float64 }

func histogram(values []int64) map[string]any {
	h := hdr.New(1, 600000000, 3)
	zeros := 0
	for _, v := range values {
		if v == 0 {
			zeros++
		}
		if h.RecordValue(max(1, v)) != nil {
			panic("histogram overflow")
		}
	}
	return map[string]any{"zero_input_count": zeros, "count": h.TotalCount(), "p50_us": h.ValueAtQuantile(50), "p99_us": h.ValueAtQuantile(99), "max_us": h.Max(), "histogram": h.Export()}
}
func main() {
	if len(os.Args) > 1 && os.Args[1] == "--vectors" {
		values := map[string][]int64{}
		if err := json.NewDecoder(os.Stdin).Decode(&values); err != nil {
			panic(err)
		}
		result := map[string]any{}
		for name, samples := range values {
			result[name] = histogram(samples)
		}
		json.NewEncoder(os.Stdout).Encode(result)
		return
	}
	rows := []map[string]any{}
	err := filepath.WalkDir(os.Args[1], func(path string, entry os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if entry.IsDir() || entry.Name() != "result.json.gz" {
			return nil
		}
		f, e := os.Open(path)
		if e != nil {
			return e
		}
		z, e := gzip.NewReader(f)
		if e != nil {
			return e
		}
		data, e := io.ReadAll(z)
		z.Close()
		f.Close()
		if e != nil {
			return e
		}
		var d struct {
			Source         string `json:"source_commit"`
			Run            string `json:"run"`
			Arm            string `json:"arm"`
			Rate, Repeat   int
			Valid, Reverse bool
			Observations   []struct {
				Lower float64 `json:"lower_ms"`
				Upper float64 `json:"upper_ms"`
				Write float64 `json:"write_call_ms"`
			}
			Flows []struct {
				Planned int64 `json:"planned_ns"`
				Write   struct {
					Done int64 `json:"call_done_ns"`
				}
			}
			Telemetry map[string]struct {
				Latency struct{ Get, Put *hdr.Snapshot }
			}
		}
		if e = json.Unmarshal(data, &d); e != nil {
			return e
		}
		upper, lower, write, planned := []int64{}, []int64{}, []int64{}, []int64{}
		for _, o := range d.Observations {
			upper = append(upper, int64(math.Ceil(o.Upper*1000)))
			lower = append(lower, int64(math.Floor(o.Lower*1000)))
			write = append(write, int64(math.Ceil(o.Write*1000)))
		}
		for _, flow := range d.Flows {
			if flow.Write.Done > 0 {
				planned = append(planned, max(1, (flow.Write.Done-flow.Planned+999)/1000))
			}
		}
		row := map[string]any{"file": strings.TrimPrefix(path, os.Args[1]+"/"), "source_commit": d.Source, "run": d.Run, "arm": d.Arm, "rate": d.Rate, "repeat": d.Repeat, "valid": d.Valid, "reverse": d.Reverse, "window_upper_us": histogram(upper), "window_lower_us": histogram(lower), "write_call_us": histogram(write), "planned_to_write_complete_us": histogram(planned)}
		server := map[string]any{}
		for name, state := range d.Telemetry {
			for method, snap := range map[string]*hdr.Snapshot{"GET": state.Latency.Get, "PUT": state.Latency.Put} {
				if snap != nil {
					h := hdr.Import(snap)
					server[name+"_"+method] = map[string]any{"count": h.TotalCount(), "p50_us": h.ValueAtQuantile(50), "p99_us": h.ValueAtQuantile(99), "max_us": h.Max()}
				}
			}
		}
		row["server"] = server
		rows = append(rows, row)
		return nil
	})
	if err != nil {
		panic(err)
	}
	json.NewEncoder(os.Stdout).Encode(rows)
}
