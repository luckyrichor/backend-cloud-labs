package datapipe

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
)

// Columnar document file read by E3 (S4/S5). Little-endian, fixed header:
//
//	magic[8] "BCLDOC1\0" | version u32 = 1 | ncols u32 | nrows u64
//	ncols × { name[24] NUL-padded | type u32 (1=f64, 2=i32) | reserved u32 | offset u64 | bytes u64 }
//	column data, each starting at an 8-byte aligned offset from file start.
const (
	DocMagic       = "BCLDOC1\x00"
	DocTypeF64     = 1
	DocTypeI32     = 2
	docHeaderBytes = 24
	docColumnBytes = 48
)

type DocColumn struct {
	Name string
	Type uint32
}

// DocColumns is the fixed schema; rule templates in P3 refer to these names.
var DocColumns = []DocColumn{
	{"price", DocTypeF64}, {"rating", DocTypeF64}, {"rating_number", DocTypeI32},
	{"category", DocTypeI32}, {"store", DocTypeI32}, {"stock", DocTypeI32},
}

type DocParams struct {
	Rows           int     `json:"rows"`
	PricePct       float64 `json:"price_perturbation"`
	RatingCountPct float64 `json:"rating_number_perturbation"`
	Sampling       string  `json:"sampling"`
}

type DocStats struct {
	Rows          int     `json:"rows"`
	BaseRows      int     `json:"base_rows"`
	Amplification float64 `json:"amplification"`
	DistinctBase  int     `json:"distinct_base_rows_drawn"`
	Price         Range   `json:"price"`
	Rating        Range   `json:"rating"`
	RatingNumber  Summary `json:"rating_number"`
	Stock         Summary `json:"stock"`
	StoreUnknown  int64   `json:"store_unknown"`
	SummaryRows   int     `json:"summaries_from_first_rows"`
}

type Range struct {
	Min  float64 `json:"min"`
	Max  float64 `json:"max"`
	Mean float64 `json:"mean"`
}

// WriteDocs resamples public rows with replacement; price ±PricePct (kept to cents),
// rating_number ±RatingCountPct, rating/category/store copied, stock synthetic.
// Columns stream to temporary files so 10^7 rows never sit in memory at once.
func WriteDocs(c *Catalog, dir, name string, seed uint64, p DocParams) (Artifact, DocStats, error) {
	r := stream(seed, fmt.Sprintf("docs/%d", p.Rows))
	tmp := make([]*os.File, len(DocColumns))
	bufs := make([]*bufio.Writer, len(DocColumns))
	defer func() {
		for _, f := range tmp {
			if f != nil {
				f.Close()
				os.Remove(f.Name())
			}
		}
	}()
	for i := range DocColumns {
		f, err := os.CreateTemp(dir, ".col-*")
		if err != nil {
			return Artifact{}, DocStats{}, err
		}
		tmp[i], bufs[i] = f, bufio.NewWriterSize(f, 1<<20)
	}
	st := DocStats{Rows: p.Rows, BaseRows: len(c.Base), Amplification: float64(p.Rows) / float64(len(c.Base))}
	st.Price.Min, st.Rating.Min = math.Inf(1), math.Inf(1)
	st.Price.Max, st.Rating.Max = math.Inf(-1), math.Inf(-1)
	seen := make([]bool, len(c.Base))
	ratingNumbers, stocks := make([]int, 0, min(p.Rows, 1<<22)), make([]int, 0, min(p.Rows, 1<<22))
	var b8 [8]byte
	var b4 [4]byte
	var sumPrice, sumRating float64
	for row := 0; row < p.Rows; row++ {
		k := r.IntN(len(c.Base))
		base := c.Base[k]
		if !seen[k] {
			seen[k] = true
			st.DistinctBase++
		}
		price := float64(perturb(base.PriceCent, r.Float64(), p.PricePct)) / 100
		count := max(0, int64(math.Round(float64(base.RatingNumber)*(1+(2*r.Float64()-1)*p.RatingCountPct))))
		stock := syntheticStock(r)
		if count > math.MaxInt32 {
			return Artifact{}, st, errors.New("rating_number exceeds i32")
		}
		f64 := []float64{price, base.Rating}
		i32 := []int32{int32(count), base.Category, base.Store, int32(stock)}
		for i, v := range f64 {
			binary.LittleEndian.PutUint64(b8[:], math.Float64bits(v))
			bufs[i].Write(b8[:])
		}
		for i, v := range i32 {
			binary.LittleEndian.PutUint32(b4[:], uint32(v))
			bufs[2+i].Write(b4[:])
		}
		sumPrice += price
		sumRating += base.Rating
		st.Price.Min, st.Price.Max = math.Min(st.Price.Min, price), math.Max(st.Price.Max, price)
		st.Rating.Min, st.Rating.Max = math.Min(st.Rating.Min, base.Rating), math.Max(st.Rating.Max, base.Rating)
		if base.Store == 0 {
			st.StoreUnknown++
		}
		// Distribution summaries use the first 4Mi rows to bound memory; noted in stats.
		if len(stocks) < cap(stocks) {
			ratingNumbers, stocks = append(ratingNumbers, int(count)), append(stocks, int(stock))
		}
	}
	st.Price.Mean, st.Rating.Mean = sumPrice/float64(p.Rows), sumRating/float64(p.Rows)
	st.RatingNumber, st.Stock = Summarize(ratingNumbers), Summarize(stocks)
	st.SummaryRows = len(stocks)
	for _, b := range bufs {
		if err := b.Flush(); err != nil {
			return Artifact{}, st, err
		}
	}

	out, err := createHashed(filepath.Join(dir, name))
	if err != nil {
		return Artifact{}, st, err
	}
	header := make([]byte, docHeaderBytes+docColumnBytes*len(DocColumns))
	copy(header, DocMagic)
	binary.LittleEndian.PutUint32(header[8:], 1)
	binary.LittleEndian.PutUint32(header[12:], uint32(len(DocColumns)))
	binary.LittleEndian.PutUint64(header[16:], uint64(p.Rows))
	offset := align8(uint64(len(header)))
	for i, col := range DocColumns {
		d := header[docHeaderBytes+i*docColumnBytes:]
		copy(d[:24], col.Name)
		binary.LittleEndian.PutUint32(d[24:], col.Type)
		width := uint64(4)
		if col.Type == DocTypeF64 {
			width = 8
		}
		binary.LittleEndian.PutUint64(d[32:], offset)
		binary.LittleEndian.PutUint64(d[40:], width*uint64(p.Rows))
		offset = align8(offset + width*uint64(p.Rows))
	}
	out.Write(header)
	written := uint64(len(header))
	for i := range DocColumns {
		pad := align8(written) - written
		out.Write(make([]byte, pad))
		written += pad
		if _, err := tmp[i].Seek(0, io.SeekStart); err != nil {
			out.Abort()
			return Artifact{}, st, err
		}
		n, err := io.Copy(out, tmp[i])
		if err != nil {
			out.Abort()
			return Artifact{}, st, err
		}
		written += uint64(n)
	}
	art, err := out.Finish(int64(p.Rows))
	return art, st, err
}

func align8(n uint64) uint64 { return (n + 7) &^ 7 }

// ReadDocHeader parses the header; used by tests and as the reference for the C++ loader.
func ReadDocHeader(r io.Reader) (rows uint64, cols []DocColumn, offsets []uint64, err error) {
	head := make([]byte, docHeaderBytes)
	if _, err = io.ReadFull(r, head); err != nil {
		return
	}
	if string(head[:8]) != DocMagic || binary.LittleEndian.Uint32(head[8:]) != 1 {
		return 0, nil, nil, errors.New("not a BCLDOC1 file")
	}
	n := binary.LittleEndian.Uint32(head[12:])
	rows = binary.LittleEndian.Uint64(head[16:])
	desc := make([]byte, docColumnBytes*int(n))
	if _, err = io.ReadFull(r, desc); err != nil {
		return
	}
	for i := 0; i < int(n); i++ {
		d := desc[i*docColumnBytes:]
		name := string(d[:24])
		for j, ch := range name {
			if ch == 0 {
				name = name[:j]
				break
			}
		}
		cols = append(cols, DocColumn{name, binary.LittleEndian.Uint32(d[24:])})
		offsets = append(offsets, binary.LittleEndian.Uint64(d[32:]))
	}
	return
}
