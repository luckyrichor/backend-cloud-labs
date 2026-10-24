package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	lg "github.com/luckyrichor/backend-cloud-labs/loadgen"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"time"
)

func fatal(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "s1":
			s1Main(os.Args[2:])
			return
		case "register":
			registerMain(os.Args[2:])
			return
		}
	}
	target := flag.String("target", "", "HTTP URL or TCP address")
	protocol := flag.String("protocol", "http", "http|tcp")
	rate := flag.Int("rate", 100, "fixed arrivals/second")
	duration := flag.Duration("duration", 3*time.Second, "arrival window")
	timeout := flag.Duration("timeout", 3*time.Second, "request timeout")
	limit := flag.Int("max-inflight", 1024, "bounded client concurrency")
	output := flag.String("output", "result.json", "raw result path")
	repo := flag.String("repo", ".", "clean repository")
	body := flag.String("body-file", "", "payload file, never copied into result")
	selftest := flag.Bool("selftest", false, "echo and one-second pause calibration")
	story := flag.String("story", "calibration", "story ID")
	arm := flag.String("arm", "baseline", "arm ID")
	dataVersion := flag.String("data-version", "none", "manifest hash")
	requireRegistry := flag.Bool("require-registry", true, "require MySQL bench_run registration")
	flag.Parse()
	commit, err := lg.RequireClean(*repo)
	fatal(err)
	if *requireRegistry && os.Getenv("LOADGEN_MYSQL_DSN") == "" {
		fatal(fmt.Errorf("LOADGEN_MYSQL_DSN required for run registry"))
	}
	var payload []byte
	if *body != "" {
		payload, err = os.ReadFile(*body)
		fatal(err)
	}
	config := lg.Config{Target: *target, Protocol: *protocol, Rate: *rate, Duration: *duration, Timeout: *timeout, MaxInflight: *limit, Body: payload, Authorization: os.Getenv("LOADGEN_AUTHORIZATION")}
	results := map[string]lg.Result{}
	if *selftest {
		for _, phase := range []string{"echo", "pause"} {
			start := time.Now()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if elapsed := time.Since(start); phase == "pause" && elapsed >= 500*time.Millisecond && elapsed < 1500*time.Millisecond {
					time.Sleep(1500*time.Millisecond - elapsed)
				}
				w.WriteHeader(204)
			}))
			config.Target = server.URL
			result, err := lg.Run(context.Background(), config)
			server.Close()
			fatal(err)
			fatal(result.Check())
			if *selftest && (!result.Valid || result.Failed != 0 || result.SendRateDeviation < -0.05 || result.SendRateDeviation > 0.05) {
				fatal(fmt.Errorf("calibration invalid or send rate deviates over 5 percent"))
			}
			results[phase] = result
		}
		if results["pause"].ResponseLatency.P99 < 700000 {
			fatal(fmt.Errorf("pause not visible in p99"))
		}
	} else {
		result, err := lg.Run(context.Background(), config)
		fatal(err)
		fatal(result.Check())
		if *selftest && (!result.Valid || result.Failed != 0 || result.SendRateDeviation < -0.05 || result.SendRateDeviation > 0.05) {
			fatal(fmt.Errorf("calibration invalid or send rate deviates over 5 percent"))
		}
		results[*arm] = result
	}
	doc := map[string]any{"git_commit": commit, "working_tree_modified": false, "data_version": *dataVersion, "generated_at": time.Now().UTC(), "calibration_only": *selftest, "results": results, "registry_required": *requireRegistry, "registry_status": "pending"}
	bytes, err := json.MarshalIndent(doc, "", "  ")
	fatal(err)
	fatal(os.MkdirAll(filepath.Dir(*output), 0755))
	fatal(os.WriteFile(*output, append(bytes, '\n'), 0644))
	if *requireRegistry {
		for phase, result := range results {
			var b [12]byte
			_, err := rand.Read(b[:])
			fatal(err)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			err = lg.Register(ctx, os.Getenv("LOADGEN_MYSQL_DSN"), hex.EncodeToString(b[:]), *story, phase, commit, *dataVersion, *output, result)
			cancel()
			fatal(err)
		}
	}
	doc["registry_status"] = "disabled"
	if *requireRegistry {
		doc["registry_status"] = "registered"
	}
	bytes, err = json.MarshalIndent(doc, "", "  ")
	fatal(err)
	fatal(os.WriteFile(*output, append(bytes, '\n'), 0644))
	fmt.Println("result written:", *output)
}
