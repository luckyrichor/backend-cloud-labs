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
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Product is one public Amazon row usable as a base: it has a title and a positive price.
type Product struct {
	Title        string
	PriceCent    int64
	Rating       float64
	RatingNumber int64
	Category     int32
	Store        int32 // 0 means the source row had no store.
}

// LoadStats records how many source rows were kept and why the rest were dropped.
type LoadStats struct {
	Files        []string       `json:"files"`
	RowsRead     int64          `json:"rows_read"`
	Kept         int64          `json:"kept"`
	DropNoPrice  int64          `json:"dropped_no_price"`
	DropBadPrice int64          `json:"dropped_unparsable_or_nonpositive_price"`
	DropNoTitle  int64          `json:"dropped_no_title"`
	KeptPerFile  map[string]int `json:"kept_per_file"`
	Categories   int            `json:"categories"`
	Stores       int            `json:"stores"`
	TitleTrimmed int64          `json:"titles_truncated"`
}

// Catalog is the deterministic base set plus its dictionaries (index i holds id i+1).
type Catalog struct {
	Base       []Product
	Categories []string
	Stores     []string
	Stats      LoadStats
}

type amazonRow struct {
	Title        string          `json:"title"`
	Price        json.RawMessage `json:"price"`
	Rating       float64         `json:"average_rating"`
	RatingNumber int64           `json:"rating_number"`
	Store        string          `json:"store"`
	MainCategory string          `json:"main_category"`
	Categories   []string        `json:"categories"`
}

const maxTitleRunes = 400

func cleanTitle(s string) (string, bool) {
	s = strings.Join(strings.FieldsFunc(s, func(r rune) bool { return r == '\t' || r == '\n' || r == '\r' }), " ")
	s = strings.TrimSpace(strings.ToValidUTF8(s, "?"))
	if utf8.RuneCountInString(s) <= maxTitleRunes {
		return s, false
	}
	return string([]rune(s)[:maxTitleRunes]), true
}

// LoadCatalog reads Amazon 2023 meta files in the given order. Category key is
// "<file label>:<category path>" (main_category when the path is empty) because leaf
// names repeat across domains; dictionaries are sorted so ids do not depend on read order.
func LoadCatalog(paths []string) (*Catalog, error) {
	type pending struct {
		p             Product
		category, sto string
	}
	var rows []pending
	stats := LoadStats{KeptPerFile: map[string]int{}}
	categories, stores := map[string]bool{}, map[string]bool{}
	for _, path := range paths {
		label := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(path), "meta_"), ".jsonl.gz")
		stats.Files = append(stats.Files, filepath.Base(path))
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		gz, err := gzip.NewReader(f)
		if err != nil {
			f.Close()
			return nil, err
		}
		sc := bufio.NewScanner(gz)
		sc.Buffer(make([]byte, 1<<20), 64<<20)
		for sc.Scan() {
			stats.RowsRead++
			var row amazonRow
			if err := json.Unmarshal(sc.Bytes(), &row); err != nil {
				f.Close()
				return nil, fmt.Errorf("%s row %d: %w", label, stats.RowsRead, err)
			}
			if raw := string(row.Price); raw == "" || raw == "null" {
				stats.DropNoPrice++
				continue
			}
			price, err := strconv.ParseFloat(string(row.Price), 64)
			if err != nil || !(price > 0) || math.IsInf(price, 0) {
				stats.DropBadPrice++
				continue
			}
			title, trimmed := cleanTitle(row.Title)
			if title == "" {
				stats.DropNoTitle++
				continue
			}
			if trimmed {
				stats.TitleTrimmed++
			}
			category := label + ":" + row.MainCategory
			if len(row.Categories) > 0 {
				category = label + ":" + strings.Join(row.Categories, " > ")
			}
			cents := int64(math.Round(price * 100))
			rows = append(rows, pending{Product{Title: title, PriceCent: max(cents, 1), Rating: row.Rating, RatingNumber: row.RatingNumber}, category, strings.TrimSpace(row.Store)})
			categories[category] = true
			if s := strings.TrimSpace(row.Store); s != "" {
				stores[s] = true
			}
			stats.KeptPerFile[label]++
		}
		err = sc.Err()
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", label, err)
		}
	}
	c := &Catalog{Categories: sortedKeys(categories), Stores: sortedKeys(stores)}
	catID, storeID := index(c.Categories), index(c.Stores)
	for _, r := range rows {
		r.p.Category = catID[r.category]
		r.p.Store = storeID[r.sto]
		c.Base = append(c.Base, r.p)
	}
	stats.Kept = int64(len(c.Base))
	stats.Categories, stats.Stores = len(c.Categories), len(c.Stores)
	c.Stats = stats
	if len(c.Base) == 0 {
		return nil, fmt.Errorf("no priced rows in %v", paths)
	}
	return c, nil
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// index assigns ids 1..n in sorted order; the empty string is absent and maps to 0.
func index(keys []string) map[string]int32 {
	m := make(map[string]int32, len(keys))
	for i, k := range keys {
		m[k] = int32(i + 1)
	}
	return m
}

// syntheticStock: 5% sold out, otherwise 1 + U[0, U[1,1000]), a decaying shape built from
// integer draws only. No public stock exists, so every stock value is synthetic.
func syntheticStock(r interface {
	Float64() float64
	IntN(int) int
}) int64 {
	if r.Float64() < 0.05 {
		return 0
	}
	return 1 + int64(r.IntN(1+r.IntN(1000)))
}

func perturb(cents int64, u float64, pct float64) int64 {
	return max(1, int64(math.Round(float64(cents)*(1+(2*u-1)*pct))))
}

// CatalogParams controls the E1 items table (S1/S2).
type CatalogParams struct {
	Rows       int     `json:"rows"`
	PricePct   float64 `json:"clone_price_perturbation"`
	UpdatedAt  string  `json:"updated_at"`
	ZipfSHint  float64 `json:"zipf_s_default"`
	Assignment string  `json:"assignment"`
}

// CatalogStats summarises the generated items for datasets/stats.
type CatalogStats struct {
	PublicRows      int64            `json:"rows_public_base"`
	SyntheticClones int64            `json:"rows_synthetic_clone"`
	Amplification   float64          `json:"amplification"`
	PriceCent       Summary          `json:"price_cent"`
	Stock           Summary          `json:"stock"`
	StockZero       int64            `json:"stock_zero"`
	SumPriceCent    int64            `json:"sum_price_cent"`
	SumStock        int64            `json:"sum_stock"`
	RowsPerDomain   map[string]int64 `json:"rows_per_domain"`
	TopCategories   []CountedKey     `json:"top_categories"`
}

type CountedKey struct {
	Key   string `json:"key"`
	Count int64  `json:"count"`
}

// WriteCatalog writes items.tsv (MySQL import order: id,title,category_id,price_cent,stock,
// version,updated_at), the category/store dictionaries and popularity.u32 — a seeded
// permutation of ids, rank 0 first, that loadgen maps Zipf ranks onto.
//
// Row i (1-based) uses base (i-1) mod B: the first B rows are the public rows unmodified
// apart from synthetic stock; later rows are clones with a new id and price ±PricePct.
func WriteCatalog(c *Catalog, dir string, seed uint64, p CatalogParams) ([]Artifact, CatalogStats, error) {
	r := stream(seed, "catalog/items")
	items, err := createHashed(filepath.Join(dir, "items.tsv"))
	if err != nil {
		return nil, CatalogStats{}, err
	}
	B := int64(len(c.Base))
	st := CatalogStats{RowsPerDomain: map[string]int64{}}
	prices, stocks := make([]int, 0, p.Rows), make([]int, 0, p.Rows)
	perCategory := make([]int64, len(c.Categories)+1)
	line := make([]byte, 0, 1024)
	for i := int64(1); i <= int64(p.Rows); i++ {
		base := c.Base[(i-1)%B]
		price := base.PriceCent
		if i > B {
			price = perturb(price, r.Float64(), p.PricePct)
			st.SyntheticClones++
		} else {
			st.PublicRows++
		}
		stock := syntheticStock(r)
		line = line[:0]
		line = strconv.AppendInt(line, i, 10)
		line = append(line, '\t')
		line = append(line, base.Title...)
		line = append(line, '\t')
		line = strconv.AppendInt(line, int64(base.Category), 10)
		line = append(line, '\t')
		line = strconv.AppendInt(line, price, 10)
		line = append(line, '\t')
		line = strconv.AppendInt(line, stock, 10)
		line = append(line, "\t1\t"...)
		line = append(line, p.UpdatedAt...)
		line = append(line, '\n')
		if _, err := items.Write(line); err != nil {
			items.Abort()
			return nil, st, err
		}
		prices, stocks = append(prices, int(price)), append(stocks, int(stock))
		st.SumPriceCent += price
		st.SumStock += stock
		if stock == 0 {
			st.StockZero++
		}
		perCategory[base.Category]++
	}
	itemsArt, err := items.Finish(int64(p.Rows))
	if err != nil {
		return nil, st, err
	}
	arts := []Artifact{itemsArt}
	for _, d := range []struct {
		name string
		keys []string
	}{{"categories.tsv", c.Categories}, {"stores.tsv", c.Stores}} {
		f, err := createHashed(filepath.Join(dir, d.name))
		if err != nil {
			return nil, st, err
		}
		for i, k := range d.keys {
			fmt.Fprintf(f, "%d\t%s\n", i+1, strings.ReplaceAll(k, "\t", " "))
		}
		a, err := f.Finish(int64(len(d.keys)))
		if err != nil {
			return nil, st, err
		}
		arts = append(arts, a)
	}
	perm := make([]uint32, p.Rows)
	for i := range perm {
		perm[i] = uint32(i + 1)
	}
	pr := stream(seed, "catalog/popularity")
	pr.Shuffle(len(perm), func(i, j int) { perm[i], perm[j] = perm[j], perm[i] })
	pop, err := createHashed(filepath.Join(dir, "popularity.u32"))
	if err != nil {
		return nil, st, err
	}
	if err := binary.Write(pop, binary.LittleEndian, perm); err != nil {
		pop.Abort()
		return nil, st, err
	}
	a, err := pop.Finish(int64(p.Rows))
	if err != nil {
		return nil, st, err
	}
	arts = append(arts, a)

	st.Amplification = float64(p.Rows) / float64(B)
	st.PriceCent, st.Stock = Summarize(prices), Summarize(stocks)
	for id, n := range perCategory[1:] {
		domain, _, _ := strings.Cut(c.Categories[id], ":")
		st.RowsPerDomain[domain] += n
	}
	st.TopCategories = topK(c.Categories, perCategory[1:], 15)
	return arts, st, nil
}

func topK(keys []string, counts []int64, k int) []CountedKey {
	out := make([]CountedKey, len(keys))
	for i := range keys {
		out[i] = CountedKey{keys[i], counts[i]}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Count > out[j].Count })
	return out[:min(k, len(out))]
}
