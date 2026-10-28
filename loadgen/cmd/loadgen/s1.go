package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/go-sql-driver/mysql"
	lg "github.com/luckyrichor/backend-cloud-labs/loadgen"
	"github.com/luckyrichor/backend-cloud-labs/support/telemetry/audit"
)

// sourceCommit is stamped at build time (-ldflags "-X main.sourceCommit=<sha>") so a copied
// binary, e.g. on the Mac, still records which clean source it was built from.
var sourceCommit = ""

func s1Main(args []string) {
	fs := flag.NewFlagSet("s1", flag.ExitOnError)
	auditRun := fs.String("audit-run", "", "run label joining all-key events with raw records")
	bypassAudit := fs.String("bypass-audit", "", "exclusive Linux commit-return audit file for bypass writes")
	target := fs.String("target", "http://127.0.0.1:18086", "E1 business base URL")
	targets := fs.String("targets", "", "equivalent business base URLs separated by commas; round-robin arrivals")
	perTarget := fs.Int("connections-per-target", 0, "HTTP connection cap per target (zero uses max-inflight)")
	admin := fs.String("admin", "http://127.0.0.1:19086", "E1 admin base URL ('' to skip server snapshots)")
	popularity := fs.String("popularity", "", "datagen catalog/popularity.u32")
	zipfS := fs.Float64("zipf-s", 0.99, "Zipf exponent over popularity ranks")
	readRatio := fs.Float64("read-ratio", 0.95, "fraction of reads")
	rate := fs.Int("rate", 500, "arrivals per second")
	warmup := fs.Duration("warmup", 20*time.Second, "discarded warmup at the same rate")
	window := fs.Duration("window", 60*time.Second, "measured window")
	seed := fs.Uint64("seed", 1, "request-plan seed (same seed = same request sequence)")
	limit := fs.Int("max-inflight", 512, "client concurrency bound")
	prime := fs.Int("prime-connections", 0, "read-only HTTP connection priming before timed arrivals")
	timeout := fs.Duration("timeout", 2*time.Second, "per-request timeout")
	output := fs.String("output", "", "result JSON path")
	raw := fs.String("raw", "", "per-request gzip CSV path")
	bypassDSN := fs.String("bypass-dsn-file", "", "file holding a MySQL DSN: send writes straight to MySQL")
	story := fs.String("story", "S1", "story id")
	arm := fs.String("arm", "", "arm id")
	repeat := fs.Int("repeat", 1, "repeat number")
	dataVersion := fs.String("data-version", "", "catalog data version")
	repo := fs.String("repo", "", "repository to check for a clean tree when the binary has no stamped commit")
	fs.Parse(args)
	if *popularity == "" || *output == "" || *arm == "" || *dataVersion == "" {
		fatal(errors.New("--popularity, --output, --arm and --data-version are required"))
	}
	commit := sourceCommit
	if commit == "" {
		if *repo == "" {
			fatal(errors.New("binary has no stamped commit; pass --repo of a clean worktree"))
		}
		var err error
		commit, err = lg.RequireClean(*repo)
		fatal(err)
	}
	pop, err := lg.LoadPopularity(*popularity)
	fatal(err)
	auth := os.Getenv("LOADGEN_AUTHORIZATION")
	cfg := lg.S1Config{Target: strings.TrimRight(*target, "/"), Admin: strings.TrimRight(*admin, "/"), Popularity: pop, ZipfS: *zipfS,
		ReadRatio: *readRatio, PrimeConnections: *prime, Rate: *rate, Warmup: *warmup, Window: *window, Seed: *seed, MaxInflight: *limit, Timeout: *timeout, Authorization: auth}
	cfg.AuditRun = *auditRun
	if *bypassAudit != "" {
		w, err := audit.Open(*bypassAudit, "bypass-writer")
		fatal(err)
		cfg.BypassAudit = w
		defer func() { fatal(w.Close()) }()
	}
	if *targets != "" {
		for _, target := range strings.Split(*targets, ",") {
			cfg.Targets = append(cfg.Targets, strings.TrimRight(strings.TrimSpace(target), "/"))
		}
		cfg.Target = cfg.Targets[0]
	}
	cfg.PerTargetConnections = *perTarget
	if *bypassDSN != "" {
		text, err := os.ReadFile(*bypassDSN)
		fatal(err)
		c, err := mysql.ParseDSN(strings.TrimSpace(string(text)))
		if err != nil {
			fatal(errors.New("bypass DSN rejected"))
		}
		db, err := sql.Open("mysql", c.FormatDSN())
		if err != nil || db.Ping() != nil {
			fatal(errors.New("bypass MySQL connection failed"))
		}
		db.SetMaxOpenConns(min(*limit, 64))
		db.SetMaxIdleConns(min(*limit, 64))
		defer db.Close()
		cfg.Bypass = db
	} else if *readRatio < 1 && auth == "" {
		fatal(errors.New("writes need LOADGEN_AUTHORIZATION=\"Bearer <token>\""))
	}
	var nofile syscall.Rlimit
	syscall.Getrlimit(syscall.RLIMIT_NOFILE, &nofile)
	if nofile.Cur < uint64(*limit)+64 {
		fatal(fmt.Errorf("nofile soft limit %d is below max-inflight+64; raise it with ulimit -n", nofile.Cur))
	}
	host, _ := os.Hostname()
	res, recs, err := lg.RunS1(context.Background(), cfg)
	fatal(err)
	if *raw != "" {
		fatal(os.MkdirAll(filepath.Dir(*raw), 0o755))
		fatal(lg.WriteS1Raw(*raw, recs))
	}
	doc := map[string]any{
		"story": *story, "arm": *arm, "repeat": *repeat, "git_commit": commit, "data_version": *dataVersion,
		"generated_at": time.Now().UTC(),
		"config": map[string]any{"zipf_s": *zipfS, "audit_run": cfg.AuditRun, "read_ratio": *readRatio, "rate": *rate, "warmup": warmup.String(),
			"window": window.String(), "seed": *seed, "max_inflight": *limit, "timeout": timeout.String(), "bypass_writes": cfg.Bypass != nil,
			"popularity_ids": len(pop), "prime_connections": *prime, "targets": cfg.Targets, "connections_per_target": *perTarget, "bypass_pool_limit": min(*limit, 64)},
		"client": map[string]any{"host": host, "goos": runtime.GOOS, "goarch": runtime.GOARCH, "cpus": runtime.NumCPU(), "nofile_soft": nofile.Cur},
		"result": res,
	}
	b, err := json.MarshalIndent(doc, "", " ")
	fatal(err)
	fatal(os.MkdirAll(filepath.Dir(*output), 0o755))
	fatal(os.WriteFile(*output, append(b, '\n'), 0o644))
	fmt.Printf("%s %s r%d rate=%d valid=%t reads=%d stale=%d (%.4f) read_p99=%dus dropped=%d\n", *story, *arm, *repeat, *rate, res.Valid,
		res.Reads.Count, res.StaleReads, res.StaleRatio, res.Reads.Latency.P99, res.Dropped)
}

// registerMain records a finished S1 result file in bench_run (run on tx, where the DSN lives).
func registerMain(args []string) {
	fs := flag.NewFlagSet("register", flag.ExitOnError)
	path := fs.String("result", "", "result JSON written by `loadgen s1`")
	recorded := fs.String("recorded-path", "", "path to store in bench_run (defaults to --result)")
	fs.Parse(args)
	raw, err := os.ReadFile(*path)
	fatal(err)
	var doc struct {
		Story, Arm, GitCommit, DataVersion string
		Repeat                             int
		Config, Client                     map[string]any
		Result                             struct {
			StartedAt time.Time `json:"started_at"`
			Valid     bool      `json:"valid"`
		}
	}
	var loose map[string]json.RawMessage
	fatal(json.Unmarshal(raw, &loose))
	json.Unmarshal(loose["story"], &doc.Story)
	json.Unmarshal(loose["arm"], &doc.Arm)
	json.Unmarshal(loose["git_commit"], &doc.GitCommit)
	json.Unmarshal(loose["data_version"], &doc.DataVersion)
	json.Unmarshal(loose["repeat"], &doc.Repeat)
	json.Unmarshal(loose["config"], &doc.Config)
	json.Unmarshal(loose["client"], &doc.Client)
	fatal(json.Unmarshal(loose["result"], &doc.Result))
	if doc.Story == "" || doc.Arm == "" || len(doc.GitCommit) != 40 {
		fatal(errors.New("result file lacks story, arm or a full commit"))
	}
	status := "completed"
	if !doc.Result.Valid {
		status = "invalid"
	}
	if *recorded == "" {
		*recorded = *path
	}
	var id [12]byte
	rand.Read(id[:])
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	fatal(lg.RegisterRun(ctx, os.Getenv("LOADGEN_MYSQL_DSN"), hex.EncodeToString(id[:]), doc.Story, doc.Arm, doc.Repeat,
		doc.GitCommit, doc.DataVersion, map[string]any{"config": doc.Config, "client": doc.Client}, *recorded, status, doc.Result.StartedAt))
	fmt.Println(hex.EncodeToString(id[:]), status)
}
