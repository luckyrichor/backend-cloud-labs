package datapipe

import (
	"bufio"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeAmazon(t *testing.T, dir, name string, rows []string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	for _, r := range rows {
		fmt.Fprintln(gz, r)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return path
}

func fixtureCatalog(t *testing.T) *Catalog {
	t.Helper()
	dir := t.TempDir()
	a := writeAmazon(t, dir, "meta_A.jsonl.gz", []string{
		`{"title":"tab\there","price":12.5,"average_rating":4.5,"rating_number":10,"store":"S2","main_category":"M","categories":[]}`,
		`{"title":"no price","price":null,"average_rating":4,"rating_number":1,"store":"S1","main_category":"M","categories":[]}`,
		`{"title":"string price","price":"$3","average_rating":4,"rating_number":1,"store":"S1","main_category":"M","categories":[]}`,
		`{"title":"","price":3,"average_rating":4,"rating_number":1,"store":"S1","main_category":"M","categories":[]}`,
	})
	b := writeAmazon(t, dir, "meta_B.jsonl.gz", []string{
		`{"title":"b1","price":0.99,"average_rating":3.1,"rating_number":7,"store":"","main_category":"M","categories":["X","Leaf"]}`,
		`{"title":"b2","price":250,"average_rating":5,"rating_number":2000,"store":"S1","main_category":"M","categories":["Y","Leaf"]}`,
	})
	c, err := LoadCatalog([]string{a, b})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestLoadCatalogKeepsOnlyPricedTitledRows(t *testing.T) {
	c := fixtureCatalog(t)
	s := c.Stats
	if s.RowsRead != 6 || s.Kept != 3 || s.DropNoPrice != 1 || s.DropBadPrice != 1 || s.DropNoTitle != 1 {
		t.Fatalf("stats %+v", s)
	}
	if c.Base[0].Title != "tab here" || c.Base[0].PriceCent != 1250 {
		t.Fatalf("first row %+v", c.Base[0])
	}
	// Leaf names repeat across paths; keys keep the full path so they stay distinct.
	want := []string{"A:M", "B:X > Leaf", "B:Y > Leaf"}
	if strings.Join(c.Categories, "|") != strings.Join(want, "|") {
		t.Fatalf("categories %q", c.Categories)
	}
	if c.Base[1].Store != 0 || c.Stores[c.Base[2].Store-1] != "S1" {
		t.Fatalf("store ids %+v %q", c.Base, c.Stores)
	}
}

func sha(t *testing.T, arts []Artifact) string {
	t.Helper()
	b, _ := json.Marshal(arts)
	return string(b)
}

func TestCatalogDeterministicAndClonesKeepBase(t *testing.T) {
	c := fixtureCatalog(t)
	p := CatalogParams{Rows: 10, PricePct: 0.05, UpdatedAt: "2026-01-01 00:00:00.000000"}
	d1, d2 := t.TempDir(), t.TempDir()
	a1, st, err := WriteCatalog(c, d1, 7, p)
	if err != nil {
		t.Fatal(err)
	}
	a2, _, err := WriteCatalog(c, d2, 7, p)
	if err != nil {
		t.Fatal(err)
	}
	if sha(t, a1) != sha(t, a2) {
		t.Fatal("same seed produced different files")
	}
	a3, _, _ := WriteCatalog(c, t.TempDir(), 8, p)
	if sha(t, a1) == sha(t, a3) {
		t.Fatal("different seed produced identical files")
	}
	if st.PublicRows != 3 || st.SyntheticClones != 7 {
		t.Fatalf("public/clone %+v", st)
	}
	f, _ := os.Open(filepath.Join(d1, "items.tsv"))
	defer f.Close()
	sc := bufio.NewScanner(f)
	for i := 1; sc.Scan(); i++ {
		cols := strings.Split(sc.Text(), "\t")
		base := c.Base[(i-1)%3]
		var price int64
		fmt.Sscan(cols[3], &price)
		if len(cols) != 7 || cols[0] != fmt.Sprint(i) || cols[1] != base.Title {
			t.Fatalf("row %d %q", i, cols)
		}
		if i <= 3 && price != base.PriceCent {
			t.Fatalf("public row %d price changed %d", i, price)
		}
		if math.Abs(float64(price-base.PriceCent)) > 0.05*float64(base.PriceCent)+1 {
			t.Fatalf("clone %d price %d outside ±5%% of %d", i, price, base.PriceCent)
		}
	}
	pop, _ := os.ReadFile(filepath.Join(d1, "popularity.u32"))
	seen := map[uint32]bool{}
	for i := 0; i < len(pop); i += 4 {
		seen[binary.LittleEndian.Uint32(pop[i:])] = true
	}
	if len(pop) != 40 || len(seen) != 10 || seen[0] {
		t.Fatalf("popularity is not a permutation of 1..10")
	}
}

func TestDocsLayoutAndDeterminism(t *testing.T) {
	c := fixtureCatalog(t)
	p := DocParams{Rows: 101, PricePct: 0.02, RatingCountPct: 0.05}
	dir := t.TempDir()
	a1, st, err := WriteDocs(c, dir, "d.bcldoc", 3, p)
	if err != nil {
		t.Fatal(err)
	}
	a2, _, _ := WriteDocs(c, t.TempDir(), "d.bcldoc", 3, p)
	if a1.SHA256 != a2.SHA256 {
		t.Fatal("docs not deterministic")
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "d.bcldoc"))
	f, _ := os.Open(filepath.Join(dir, "d.bcldoc"))
	defer f.Close()
	rows, cols, offsets, err := ReadDocHeader(f)
	if err != nil || rows != 101 || len(cols) != len(DocColumns) {
		t.Fatalf("header rows=%d cols=%v err=%v", rows, cols, err)
	}
	end := offsets[len(offsets)-1] + 4*rows
	if uint64(len(raw)) != end {
		t.Fatalf("file size %d, expected %d", len(raw), end)
	}
	for i, off := range offsets {
		if off%8 != 0 || cols[i] != DocColumns[i] {
			t.Fatalf("column %d %v at %d", i, cols[i], off)
		}
	}
	for r := uint64(0); r < rows; r++ {
		price := math.Float64frombits(binary.LittleEndian.Uint64(raw[offsets[0]+8*r:]))
		category := int32(binary.LittleEndian.Uint32(raw[offsets[3]+4*r:]))
		if price < 0.01 || category < 1 || int(category) > len(c.Categories) {
			t.Fatalf("row %d price %v category %d", r, price, category)
		}
	}
	if st.Price.Min < 0.99*0.98-0.01 || st.Price.Max > 250*1.02+0.01 {
		t.Fatalf("price range %+v", st.Price)
	}
}

func testDegrees() *DegreeStats {
	d := &DegreeStats{}
	d.Alibaba.ServiceOut = [][2]int{{0, 4}, {1, 3}, {3, 2}, {200, 1}}
	d.Alibaba.ServiceIn = [][2]int{{0, 1}, {1, 5}, {4, 3}}
	d.Alibaba.StorageOut = [][2]int{{0, 3}, {2, 5}, {9, 1}}
	d.Alibaba.StorageIn = [][2]int{{1, 6}, {3, 2}}
	d.Alibaba.Services, d.Alibaba.StorageNodes = 10, 30
	return d
}

func TestCMDBInvariants(t *testing.T) {
	p := CMDBParams{CIs: 2000, Tenants: 4, HostShare: .12, ServiceShare: .12, DatastoreShare: .36}
	dir := t.TempDir()
	arts, st, err := WriteCMDB(testDegrees(), dir, "g", 5, p)
	if err != nil {
		t.Fatal(err)
	}
	again, _, _ := WriteCMDB(testDegrees(), t.TempDir(), "g", 5, p)
	if sha(t, arts) != sha(t, again) {
		t.Fatal("cmdb not deterministic")
	}
	tenant, kind := map[int64]string{}, map[int64]string{}
	ciFile, _ := os.Open(filepath.Join(dir, "g-cis.jsonl"))
	defer ciFile.Close()
	for sc := bufio.NewScanner(ciFile); sc.Scan(); {
		var c ciRecord
		json.Unmarshal(sc.Bytes(), &c)
		tenant[c.ID], kind[c.ID] = c.Tenant, c.Kind
	}
	if len(tenant) != 2000 {
		t.Fatalf("cis %d", len(tenant))
	}
	relFile, _ := os.Open(filepath.Join(dir, "g-relations.jsonl"))
	defer relFile.Close()
	seen := map[[2]int64]bool{}
	instance, runs := map[int64]int{}, map[int64]int{}
	allowed := map[string][2]string{"instance_of": {"container", "service"}, "depends_on": {"service", "service"}, "uses": {"service", "datastore"}}
	for sc := bufio.NewScanner(relFile); sc.Scan(); {
		var r relRecord
		json.Unmarshal(sc.Bytes(), &r)
		if r.From == r.To || seen[[2]int64{r.From, r.To}] {
			t.Fatalf("self or duplicate edge %+v", r)
		}
		seen[[2]int64{r.From, r.To}] = true
		if tenant[r.From] != r.Tenant || tenant[r.To] != r.Tenant {
			t.Fatalf("cross-tenant edge %+v", r)
		}
		if want, ok := allowed[r.Kind]; ok && (kind[r.From] != want[0] || kind[r.To] != want[1]) {
			t.Fatalf("edge kind %+v from %s to %s", r, kind[r.From], kind[r.To])
		}
		if r.Kind == "runs_on" && kind[r.To] != "host" {
			t.Fatalf("runs_on target %s", kind[r.To])
		}
		switch r.Kind {
		case "instance_of":
			instance[r.From]++
		case "runs_on":
			runs[r.From]++
		}
	}
	for id, k := range kind {
		if k == "container" && (instance[id] != 1 || runs[id] != 1) {
			t.Fatalf("container %d instance_of=%d runs_on=%d", id, instance[id], runs[id])
		}
		if k == "datastore" && runs[id] != 1 {
			t.Fatalf("datastore %d runs_on=%d", id, runs[id])
		}
	}
	// 60 services per tenant: a degree-200 draw exceeds the available targets and is capped.
	if st.CappedDraws == 0 || st.CappedEdgesLost == 0 {
		t.Fatalf("expected capped draws to be recorded: %+v", st)
	}
}

func readTrack(t *testing.T, path string) (bots, frames int, intents []float32) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw[:8]) != TrackMagic {
		t.Fatal("bad magic")
	}
	bots, frames = int(binary.LittleEndian.Uint32(raw[16:])), int(binary.LittleEndian.Uint32(raw[20:]))
	body := raw[trackHeaderBytes:]
	if len(body) != frames*bots*8 {
		t.Fatalf("body %d bytes", len(body))
	}
	for i := 0; i < len(body); i += 4 {
		intents = append(intents, math.Float32frombits(binary.LittleEndian.Uint32(body[i:])))
	}
	return
}

func TestTracksPrefixAndSpeed(t *testing.T) {
	for _, pattern := range []uint32{TrackWaypoint, TrackHotspot} {
		p := TrackParams{Pattern: pattern, Bots: 20, Frames: 300, World: 60, Speed: 1, MaxDwell: 5, Hotspots: 2, HotShare: .8, HotSpread: .03}
		dir := t.TempDir()
		if _, _, err := WriteTrack(dir, "big", 9, p, []float64{5}); err != nil {
			t.Fatal(err)
		}
		p.Bots = 8
		if _, _, err := WriteTrack(dir, "small", 9, p, []float64{5}); err != nil {
			t.Fatal(err)
		}
		bigBots, frames, big := readTrack(t, filepath.Join(dir, "big"))
		smallBots, _, small := readTrack(t, filepath.Join(dir, "small"))
		for f := 0; f < frames; f++ {
			for b := 0; b < smallBots; b++ {
				for k := 0; k < 2; k++ {
					if small[(f*smallBots+b)*2+k] != big[(f*bigBots+b)*2+k] {
						t.Fatalf("pattern %d: bot %d frame %d differs between tiers", pattern, b, f)
					}
				}
			}
		}
		for i := 0; i < len(big); i += 2 {
			if math.Hypot(float64(big[i]), float64(big[i+1])) > 1+1e-6 {
				t.Fatalf("intent %d longer than one unit", i/2)
			}
		}
	}
}

func TestHistogramSampleMatchesSupport(t *testing.T) {
	h := NewHistogram([][2]int{{0, 0}, {2, 1}, {5, 3}})
	if h.Mean() != (2+15)/4.0 {
		t.Fatalf("mean %v", h.Mean())
	}
	r := stream(1, "h")
	for range 1000 {
		if v := h.Sample(r); v != 2 && v != 5 {
			t.Fatalf("sampled %d", v)
		}
	}
}

func TestSplitSQLAndLoadMigrations(t *testing.T) {
	got := SplitSQL("-- header\nCREATE TABLE a (\n  x INT -- not a full-line comment\n);\n\n-- c\nDROP TABLE b;\n")
	if len(got) != 2 || !strings.HasPrefix(got[0], "CREATE TABLE a (") || got[1] != "DROP TABLE b" {
		t.Fatalf("split %q", got)
	}
	all, err := LoadMigrations("../../deploy/mysql/migrations")
	if err != nil {
		t.Fatal(err)
	}
	created, dropped := map[string]bool{}, map[string]bool{}
	tableName := func(stmt, verb string) string {
		f := strings.Fields(strings.TrimPrefix(stmt, verb))
		return f[0]
	}
	for _, m := range all {
		for _, s := range m.Up {
			if strings.HasPrefix(s, "CREATE TABLE ") {
				created[tableName(s, "CREATE TABLE ")] = true
			}
		}
		for _, s := range m.Down {
			dropped[tableName(s, "DROP TABLE ")] = true
		}
	}
	// Every table a migration creates is dropped by its down file, and bench_run is never touched.
	for name := range created {
		if !dropped[name] {
			t.Fatalf("table %s has no down", name)
		}
	}
	if len(created) != len(dropped) || created["bench_run"] || dropped["bench_run"] {
		t.Fatalf("created %v dropped %v", created, dropped)
	}
	bad := t.TempDir()
	os.WriteFile(filepath.Join(bad, "0001_a.up.sql"), []byte("CREATE TABLE a (x INT);"), 0o644)
	if _, err := LoadMigrations(bad); err == nil {
		t.Fatal("migration without down accepted")
	}
}
