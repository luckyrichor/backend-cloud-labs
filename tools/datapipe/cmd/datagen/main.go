// datagen produces the P1 datasets: E1 catalog, E3 documents, E5 CMDB graphs, E2 tracks.
//
//	go -C tools/datapipe run ./cmd/datagen --repo ../.. --out /home/ubuntu/datasets/backend-cloud-labs/p1 \
//	    --amazon-dir /home/ubuntu/datasets/backend-cloud-labs/p0
//
// Raw outputs go under --out (outside the repository); manifests also go to
// <repo>/datasets/stats/<dataset>.json. A dirty working tree is refused unless --development.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	dp "github.com/luckyrichor/backend-cloud-labs/tools/datapipe"
)

var amazonFiles = []string{"meta_All_Beauty.jsonl.gz", "meta_Appliances.jsonl.gz", "meta_Video_Games.jsonl.gz"}

func main() {
	repo := flag.String("repo", ".", "repository root (for commit, stats and degree histogram)")
	out := flag.String("out", "", "output directory outside the repository")
	amazonDir := flag.String("amazon-dir", "", "directory holding the Amazon meta files")
	seed := flag.Uint64("seed", 20261023, "random seed")
	only := flag.String("only", "catalog,docs,cmdb,tracks", "comma-separated datasets")
	scale := flag.String("scale", "full", "full, or small for a quick development run")
	statsDir := flag.String("stats-dir", "", "manifest copy directory (default <repo>/datasets/stats; 'none' to skip)")
	development := flag.Bool("development", false, "allow a dirty working tree; output is marked dirty")
	flag.Parse()
	if *out == "" {
		fatal(fmt.Errorf("--out required"))
	}
	root, _ := filepath.Abs(*repo)
	dest, _ := filepath.Abs(*out)
	if dest == root || strings.HasPrefix(dest, root+string(os.PathSeparator)) {
		fatal(fmt.Errorf("raw outputs must be outside the repository"))
	}
	commit, dirty, err := dp.GitState(root)
	if err != nil {
		fatal(err)
	}
	if dirty && !*development {
		fatal(fmt.Errorf("working tree modified; commit first or pass --development"))
	}
	if *statsDir == "" {
		*statsDir = filepath.Join(root, "datasets", "stats")
	}
	small := *scale == "small"
	want := strings.Split(*only, ",")
	base := dp.Manifest{Generator: "tools/datapipe/cmd/datagen", GeneratorCommit: commit, WorkingTreeDirty: dirty, Seed: *seed}

	publish := func(m dp.Manifest) {
		if err := m.Seal(); err != nil {
			fatal(err)
		}
		if err := dp.WriteJSON(filepath.Join(dest, m.Dataset, "manifest.json"), m); err != nil {
			fatal(err)
		}
		if *statsDir != "none" {
			if err := os.MkdirAll(*statsDir, 0o755); err != nil {
				fatal(err)
			}
			if err := dp.WriteJSON(filepath.Join(*statsDir, m.Dataset+".json"), m); err != nil {
				fatal(err)
			}
		}
		fmt.Printf("%s %s\n", m.Dataset, m.DataVersion)
	}
	mkdir := func(name string) string {
		d := filepath.Join(dest, name)
		if err := os.MkdirAll(d, 0o755); err != nil {
			fatal(err)
		}
		return d
	}

	var catalog *dp.Catalog
	var amazonInputs []dp.Input
	if slices.Contains(want, "catalog") || slices.Contains(want, "docs") {
		if *amazonDir == "" {
			fatal(fmt.Errorf("--amazon-dir required for catalog/docs"))
		}
		var paths []string
		for _, f := range amazonFiles {
			p := filepath.Join(*amazonDir, f)
			in, err := dp.HashFile(p)
			if err != nil {
				fatal(err)
			}
			paths, amazonInputs = append(paths, p), append(amazonInputs, in)
		}
		start := time.Now()
		if catalog, err = dp.LoadCatalog(paths); err != nil {
			fatal(err)
		}
		fmt.Fprintf(os.Stderr, "loaded %d priced rows in %s\n", len(catalog.Base), time.Since(start).Round(time.Millisecond))
	}

	if slices.Contains(want, "catalog") {
		rows := 2_000_000
		if small {
			rows = 50_000
		}
		p := dp.CatalogParams{Rows: rows, PricePct: 0.05, UpdatedAt: "2026-01-01 00:00:00.000000", ZipfSHint: 0.99,
			Assignment: "row i uses base (i-1) mod B; rows 1..B public, later rows synthetic clones"}
		arts, st, err := dp.WriteCatalog(catalog, mkdir("catalog"), *seed, p)
		if err != nil {
			fatal(err)
		}
		m := base
		m.Dataset, m.Source, m.Inputs, m.Params, m.Artifacts = "catalog", "public+synthetic", amazonInputs, p, arts
		m.Stats = map[string]any{"load": catalog.Stats, "items": st}
		m.Notes = []string{"stock is synthetic for every row; Amazon metadata has no stock",
			"zipf_s_default is a starting value for loadgen, which records the value it actually uses"}
		publish(m)
	}

	if slices.Contains(want, "docs") {
		sizes := []int{100_000, 1_000_000, 10_000_000}
		if small {
			sizes = []int{10_000}
		}
		d := mkdir("docs")
		var arts []dp.Artifact
		stats := map[string]dp.DocStats{}
		for _, n := range sizes {
			p := dp.DocParams{Rows: n, PricePct: 0.02, RatingCountPct: 0.05, Sampling: "uniform with replacement over priced public rows"}
			a, st, err := dp.WriteDocs(catalog, d, fmt.Sprintf("docs-%d.bcldoc", n), *seed, p)
			if err != nil {
				fatal(err)
			}
			arts, stats[fmt.Sprint(n)] = append(arts, a), st
		}
		m := base
		m.Dataset, m.Source, m.Inputs, m.Artifacts, m.Stats = "docs", "public-resampled+synthetic", amazonInputs, arts, stats
		m.Params = map[string]any{"sizes": sizes, "price_perturbation": 0.02, "rating_number_perturbation": 0.05,
			"columns": dp.DocColumns, "format": "BCLDOC1, see tools/datapipe/docs.go"}
		m.Notes = []string{"stock is synthetic; other columns come from resampled public rows with small numeric perturbation"}
		publish(m)
	}

	if slices.Contains(want, "cmdb") {
		degPath := filepath.Join(root, "datasets", "stats", "graph-degree.json")
		deg, err := dp.LoadDegreeStats(degPath)
		if err != nil {
			fatal(err)
		}
		in, err := dp.HashFile(degPath)
		if err != nil {
			fatal(err)
		}
		sizes := []int{10_000, 100_000, 1_000_000}
		if small {
			sizes = []int{10_000}
		}
		storeShare := 0.12 * float64(deg.Alibaba.StorageNodes) / float64(deg.Alibaba.Services)
		d := mkdir("cmdb")
		var arts []dp.Artifact
		stats := map[string]dp.CMDBStats{}
		var params []dp.CMDBParams
		for _, n := range sizes {
			p := dp.CMDBParams{CIs: n, Tenants: 10, HostShare: 0.12, ServiceShare: 0.12, DatastoreShare: storeShare}
			a, st, err := dp.WriteCMDB(deg, d, fmt.Sprintf("cmdb-%d", n), *seed, p)
			if err != nil {
				fatal(err)
			}
			arts, stats[fmt.Sprint(n)], params = append(arts, a...), st, append(params, p)
		}
		m := base
		m.Dataset, m.Source, m.Inputs, m.Params, m.Artifacts, m.Stats = "cmdb", "synthetic(shape: alibaba callgraph degrees)", []dp.Input{in}, params, arts, stats
		m.Notes = []string{"degree shapes come from the Alibaba microservice call graph; layering, tenants and attributes are invented",
			"conclusions apply to this layered dependency graph only, not to real CMDBs"}
		publish(m)
	}

	if slices.Contains(want, "tracks") {
		bots, frames := 2000, 2400
		if small {
			bots, frames = 128, 200
		}
		d := mkdir("tracks")
		var arts []dp.Artifact
		var stats []dp.TrackStats
		var params []dp.TrackParams
		for _, pat := range []struct {
			id   uint32
			name string
		}{{dp.TrackWaypoint, "waypoint"}, {dp.TrackHotspot, "hotspot"}} {
			p := dp.TrackParams{Pattern: pat.id, Bots: bots, Frames: frames, World: 400, Speed: 1, MaxDwell: 40, Hotspots: 4, HotShare: 0.8, HotSpread: 0.03}
			a, st, err := dp.WriteTrack(d, "track-"+pat.name+".bcltrk", *seed, p, []float64{25, 50})
			if err != nil {
				fatal(err)
			}
			arts, stats, params = append(arts, a), append(stats, st), append(params, p)
		}
		m := base
		m.Dataset, m.Source, m.Params, m.Artifacts, m.Stats = "tracks", "synthetic", params, arts, stats
		m.Notes = []string{"synthetic bot movement; does not represent real player behaviour",
			"tiers 64..2000 are prefixes of the same file: bot b draws only from its own stream",
			"rows in artifacts count frames; 50 ms per frame"}
		publish(m)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "datagen:", err)
	os.Exit(1)
}
