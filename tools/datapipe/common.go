// Package datapipe generates the P1 evaluation datasets deterministically.
//
// Every generator draws from named PCG streams derived from one seed, iterates in a fixed
// order and never ranges over maps while producing output, so the same seed, inputs and
// generator commit give byte-identical files on the same architecture (tx, linux/amd64).
// Floating-point results are not promised to match across architectures: the Go compiler
// may fuse multiply-add on arm64.
package datapipe

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"hash"
	"hash/fnv"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
)

// Artifact describes one generated file; Path is relative to the output directory.
type Artifact struct {
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
	Rows   int64  `json:"rows"`
}

// hashedFile writes to a .partial file and publishes it by rename only after a successful
// flush and fsync, so an interrupted run never leaves a plausible-looking output behind.
type hashedFile struct {
	path, partial string
	file          *os.File
	buf           *bufio.Writer
	sum           hash.Hash
	bytes         int64
}

func createHashed(path string) (*hashedFile, error) {
	partial := path + ".partial"
	f, err := os.OpenFile(partial, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	h := &hashedFile{path: path, partial: partial, file: f, sum: sha256.New()}
	h.buf = bufio.NewWriterSize(io.MultiWriter(f, h.sum), 1<<20)
	return h, nil
}

func (h *hashedFile) Write(p []byte) (int, error) {
	n, err := h.buf.Write(p)
	h.bytes += int64(n)
	return n, err
}

func (h *hashedFile) WriteString(s string) (int, error) {
	n, err := h.buf.WriteString(s)
	h.bytes += int64(n)
	return n, err
}

// Finish publishes the file; rows is recorded verbatim in the artifact.
func (h *hashedFile) Finish(rows int64) (Artifact, error) {
	err := errors.Join(h.buf.Flush(), h.file.Sync(), h.file.Close())
	if err == nil {
		err = os.Rename(h.partial, h.path)
	}
	if err != nil {
		os.Remove(h.partial)
		return Artifact{}, err
	}
	return Artifact{Path: filepath.Base(h.path), Bytes: h.bytes, SHA256: hex.EncodeToString(h.sum.Sum(nil)), Rows: rows}, nil
}

// Abort discards an unfinished file.
func (h *hashedFile) Abort() {
	h.file.Close()
	os.Remove(h.partial)
}

// stream returns an independent generator for one named purpose. Adding a new stream never
// shifts the values drawn by an existing one, which keeps unrelated outputs stable.
func stream(seed uint64, name string) *rand.Rand {
	f := fnv.New64a()
	f.Write([]byte(name))
	return rand.New(rand.NewPCG(seed, f.Sum64()))
}

// Histogram is an empirical [value, count] distribution, sampled by inverse CDF.
type Histogram struct {
	values []int
	cum    []int64
}

func NewHistogram(pairs [][2]int) Histogram {
	var h Histogram
	var total int64
	for _, p := range pairs {
		if p[1] <= 0 {
			continue
		}
		total += int64(p[1])
		h.values = append(h.values, p[0])
		h.cum = append(h.cum, total)
	}
	return h
}

func (h Histogram) Sample(r *rand.Rand) int {
	x := r.Int64N(h.cum[len(h.cum)-1])
	return h.values[sort.Search(len(h.cum), func(i int) bool { return h.cum[i] > x })]
}

func (h Histogram) Mean() float64 {
	var sum, prev int64
	for i, v := range h.values {
		sum += int64(v) * (h.cum[i] - prev)
		prev = h.cum[i]
	}
	return float64(sum) / float64(h.cum[len(h.cum)-1])
}

// Summary gives distribution facts used by stats files, computed on a sorted copy.
type Summary struct {
	N    int     `json:"n"`
	Mean float64 `json:"mean"`
	P50  int     `json:"p50"`
	P90  int     `json:"p90"`
	P99  int     `json:"p99"`
	Max  int     `json:"max"`
	Zero int     `json:"zero"`
}

func Summarize(values []int) Summary {
	if len(values) == 0 {
		return Summary{}
	}
	s := append([]int(nil), values...)
	sort.Ints(s)
	var sum int64
	zero := 0
	for _, v := range s {
		sum += int64(v)
		if v == 0 {
			zero++
		}
	}
	at := func(q float64) int { return s[min(len(s)-1, int(q*float64(len(s))))] }
	return Summary{N: len(s), Mean: float64(sum) / float64(len(s)), P50: at(.5), P90: at(.9), P99: at(.99), Max: s[len(s)-1], Zero: zero}
}
