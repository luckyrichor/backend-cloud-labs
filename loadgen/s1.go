package loadgen

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/luckyrichor/backend-cloud-labs/support/telemetry/audit"
	"io"
	"math"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	hdr "github.com/HdrHistogram/hdrhistogram-go"
)

// S1 drives the E1 cache-strategy comparison (plan §E S1): open-loop arrivals, each a read
// or write of a Zipf-ranked item, decided in advance from a seed so every arm replays the
// identical request sequence. Every request is recorded; stale reads are judged against the
// writes this same process saw acknowledged, so no cross-machine clock is involved.
type S1Config struct {
	AuditRun             string
	BypassAudit          *audit.Writer
	Target, Admin        string   // business and admin base URLs, e.g. http://127.0.0.1:18086
	Targets              []string // equivalent business URLs, round-robin by planned request index
	PerTargetConnections int      // cap each forwarding transport independently (zero uses MaxInflight)
	Popularity           []uint32
	ZipfS                float64
	ReadRatio            float64
	Rate                 int
	Warmup               time.Duration
	Window               time.Duration
	Seed                 uint64
	PrimeConnections     int // optional read-only connection priming before timed arrivals
	MaxInflight          int
	Timeout              time.Duration
	Authorization        string
	// Bypass, when set, sends writes straight to MySQL instead of through the service
	// (the plan's separate "direct MySQL writes" group).
	Bypass *sql.DB
}

// LoadPopularity reads datagen's popularity.u32: rank r → item id, little-endian.
func LoadPopularity(path string) ([]uint32, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 || len(raw)%4 != 0 {
		return nil, errors.New("popularity file must hold little-endian uint32 ids")
	}
	out := make([]uint32, len(raw)/4)
	for i := range out {
		out[i] = binary.LittleEndian.Uint32(raw[4*i:])
	}
	return out, nil
}

// Zipf samples ranks 0..n-1 with P(r) ∝ 1/(r+1)^s by inverse CDF; any s ≥ 0 is allowed
// (math/rand's Zipf requires s > 1, which excludes the common 0.99).
type Zipf struct{ cum []float64 }

func NewZipf(n int, s float64) Zipf {
	cum := make([]float64, n)
	total := 0.0
	for r := range n {
		total += math.Pow(float64(r+1), -s)
		cum[r] = total
	}
	return Zipf{cum}
}

func (z Zipf) Sample(r *rand.Rand) int {
	x := r.Float64() * z.cum[len(z.cum)-1]
	return sort.SearchFloat64s(z.cum, x)
}

type s1Op struct {
	id    uint32
	write bool
}

// PlanS1 is deterministic in (seed, n, popularity, s, read ratio).
func PlanS1(seed uint64, n int, popularity []uint32, s, readRatio float64) []s1Op {
	r := rand.New(rand.NewPCG(seed, 0x51))
	z := NewZipf(len(popularity), s)
	plan := make([]s1Op, n)
	for i := range plan {
		plan[i] = s1Op{write: r.Float64() >= readRatio, id: popularity[z.Sample(r)]}
	}
	return plan
}

// S1Record is one request; times are nanoseconds since the run's monotonic start.
type S1Record struct {
	Planned, Sent, Done   int64
	ID                    uint32
	Write, Window, Issued bool
	Status                int
	Version               int64
	Source                string
	Err                   string
}

type S1OpStats struct {
	Count, Success, Failed int64
	Latency                Summary       `json:"latency_us"`
	Histogram              *hdr.Snapshot `json:"histogram"`
}

type S1Result struct {
	StartedAt         time.Time      `json:"started_at"`
	Scheduled         int            `json:"scheduled"`
	WarmupScheduled   int            `json:"warmup_scheduled"`
	Dropped           int            `json:"dropped"`
	WarmupDropped     int            `json:"warmup_dropped"`
	HistogramOverflow int            `json:"histogram_overflow"`
	ObservedRate      float64        `json:"observed_window_send_rate"`
	Reads             S1OpStats      `json:"reads"`
	Writes            S1OpStats      `json:"writes"`
	StaleReads        int64          `json:"stale_reads"`
	StaleRatio        float64        `json:"stale_ratio"`
	StaleAge          Summary        `json:"stale_age_us"`
	ReadsOfWrittenIDs int64          `json:"reads_of_written_ids"`
	ClientCacheServed int64          `json:"reads_served_from_cache"`
	Server            map[string]any `json:"server"`
	Valid             bool           `json:"valid"`
	Definitions       []string       `json:"definitions"`
	InvalidReasons    []string       `json:"invalid_reasons,omitempty"`
}

type s1Clients struct {
	http *http.Client
	cfg  S1Config
}

var writeTitlePad = strings.Repeat("x", 96)

func (c s1Clients) do(ctx context.Context, op s1Op, seq int, rec *S1Record) {
	id := strconv.FormatUint(uint64(op.id), 10)
	phase := "warmup"
	if rec.Window {
		phase = "window"
	}
	ctx = audit.WithLabels(ctx, c.cfg.AuditRun, phase, strconv.Itoa(seq))
	if op.write && c.cfg.Bypass != nil {
		price, stock := 100+int64(seq%100000), int64(seq%997)
		res, err := c.cfg.Bypass.ExecContext(ctx, `UPDATE items SET price_cent = ?, stock = ?, version = LAST_INSERT_ID(version + 1), updated_at = UTC_TIMESTAMP(6) WHERE id = ?`, price, stock, op.id)
		committedAt := audit.Now()
		if err == nil {
			var rows int64
			rows, err = res.RowsAffected()
			if err == nil && rows != 1 {
				err = errors.New("write did not update one item")
			}
			if err == nil {
				rec.Version, err = res.LastInsertId()
			}
		}
		if err != nil {
			rec.Err = "mysql"
			return
		}
		if c.cfg.BypassAudit != nil {
			c.cfg.BypassAudit.RecordAt(ctx, id, "commit_return", 0, rec.Version, committedAt)
		}
		rec.Status = 200
		return
	}
	base := c.cfg.Target
	if len(c.cfg.Targets) > 0 {
		base = c.cfg.Targets[seq%len(c.cfg.Targets)]
	}
	var req *http.Request
	var err error
	if op.write {
		// Fixed-width title keeps written rows near the catalogue's typical payload size.
		body := fmt.Sprintf(`{"title":"s1 write %d %s","price_cent":%d,"stock":%d}`, seq, writeTitlePad, 100+seq%100000, seq%997)
		req, err = http.NewRequestWithContext(ctx, http.MethodPut, base+"/items/"+id, strings.NewReader(body))
		if err == nil {
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", c.cfg.Authorization)
		}
	} else {
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, base+"/items/"+id, nil)
	}
	if err != nil {
		rec.Err = "request"
		return
	}
	req.Header.Set("X-BCL-Run", c.cfg.AuditRun)
	req.Header.Set("X-BCL-Phase", phase)
	req.Header.Set("X-BCL-Request", strconv.Itoa(seq))
	resp, err := c.http.Do(req)
	if err != nil {
		rec.Err = "transport_other"
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			rec.Err = "transport_timeout"
		case errors.Is(err, io.EOF):
			rec.Err = "transport_eof"
		case errors.Is(err, io.ErrUnexpectedEOF):
			rec.Err = "transport_unexpected_eof"
		case errors.Is(err, syscall.ECONNRESET):
			rec.Err = "transport_reset"
		case errors.Is(err, syscall.EPIPE), errors.Is(err, net.ErrClosed):
			rec.Err = "transport_closed"
		}
		return
	}
	defer resp.Body.Close()
	rec.Status = resp.StatusCode
	var body struct {
		Item struct {
			Version int64 `json:"version"`
		} `json:"item"`
		Source string `json:"source"`
	}
	if resp.StatusCode == 200 {
		if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body) != nil {
			rec.Err = "decode"
			return
		}
		rec.Version, rec.Source = body.Item.Version, body.Source
		if rec.Version <= 0 {
			rec.Err = "version"
		}
	} else {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	}
}

// RunS1 performs warmup then window back to back at one rate. Admin snapshots bracket the
// window: warmup drains before reset; the measured requests drain before the final snapshot.
func RunS1(ctx context.Context, c S1Config) (S1Result, []S1Record, error) {
	if c.Rate <= 0 || c.Window <= 0 || c.Warmup < 0 || c.MaxInflight <= 0 || c.Timeout <= 0 || len(c.Popularity) == 0 ||
		c.ReadRatio < 0 || c.ReadRatio > 1 || c.ZipfS < 0 ||
		math.IsNaN(c.ReadRatio) || math.IsInf(c.ReadRatio, 0) || math.IsNaN(c.ZipfS) || math.IsInf(c.ZipfS, 0) ||
		c.Rate > 100000 || c.MaxInflight > 100000 || c.PrimeConnections < 0 || c.PrimeConnections > c.MaxInflight || c.PerTargetConnections < 0 || c.PerTargetConnections > c.MaxInflight {
		return S1Result{}, nil, errors.New("invalid S1 configuration")
	}
	targets := append([]string(nil), c.Targets...)
	if len(targets) == 0 {
		targets = []string{c.Target}
	}
	for _, target := range targets {
		u, err := url.Parse(target)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return S1Result{}, nil, errors.New("invalid S1 business target")
		}
	}
	c.Targets = targets
	warmCount := float64(c.Rate) * c.Warmup.Seconds()
	windowCount := float64(c.Rate) * c.Window.Seconds()
	if windowCount < 1 || warmCount+windowCount > 20_000_000 {
		return S1Result{}, nil, errors.New("request count outside supported range")
	}
	warm := int(warmCount)
	total := warm + int(windowCount)
	plan := PlanS1(c.Seed, total, c.Popularity, c.ZipfS, c.ReadRatio)
	records := make([]S1Record, total)
	perTarget := c.PerTargetConnections
	if perTarget == 0 {
		perTarget = c.MaxInflight
	}
	transport := &http.Transport{MaxIdleConns: c.MaxInflight, MaxIdleConnsPerHost: perTarget, MaxConnsPerHost: perTarget, IdleConnTimeout: 2 * time.Second}
	defer transport.CloseIdleConnections()
	clients := s1Clients{&http.Client{Transport: transport, Timeout: c.Timeout}, c}
	if c.PrimeConnections > 0 {
		// Establish SSH-forwarded HTTP channels before timed arrivals. Only GETs of
		// a fixed popularity prefix are issued; no source writes can become ambiguous.
		primeClient := s1Clients{&http.Client{Transport: transport, Timeout: 15 * time.Second}, c}
		failed := make(chan bool, c.PrimeConnections)
		var primes sync.WaitGroup
		for i := range c.PrimeConnections {
			primes.Add(1)
			go func(i int) {
				defer primes.Done()
				var r S1Record
				primeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
				defer cancel()
				primeClient.do(primeCtx, s1Op{id: c.Popularity[i%len(c.Popularity)]}, i, &r)
				failed <- r.Err != "" || r.Status != 200
			}(i)
		}
		primes.Wait()
		close(failed)
		for bad := range failed {
			if bad {
				return S1Result{}, nil, errors.New("read-only HTTP connection priming failed")
			}
		}
	}
	admin := &http.Client{Timeout: 10 * time.Second}
	res := S1Result{Scheduled: total - warm, WarmupScheduled: warm, Server: map[string]any{}}

	slots := make(chan struct{}, c.MaxInflight)
	var wg sync.WaitGroup
	start := time.Now()
	res.StartedAt = start.UTC()
	windowStart := start.Add(time.Duration(float64(warm) * float64(time.Second) / float64(c.Rate)))
	var before map[string]float64
	telemetryOK := true
	for i := 0; i < total; i++ {
		if i == warm {
			// Drain warmup before resetting the server histogram. No warmup request
			// can finish inside the measured server window.
			wg.Wait()
			if c.Admin != "" {
				telemetryOK = validLatency(fetch(admin, c.Admin+"/debug/latency?reset=1"))
				before = counters(admin, c.Admin)
				telemetryOK = telemetryOK && validCounters(before)
			}
			windowStart = time.Now()
		}
		planned := start.Add(time.Duration(float64(i) * float64(time.Second) / float64(c.Rate)))
		if i >= warm {
			planned = windowStart.Add(time.Duration(float64(i-warm) * float64(time.Second) / float64(c.Rate)))
		}
		if d := time.Until(planned); d > 0 {
			select {
			case <-time.After(d):
			case <-ctx.Done():
				wg.Wait()
				return res, records, ctx.Err()
			}
		}
		rec := &records[i]
		rec.ID, rec.Write, rec.Window = plan[i].id, plan[i].write, i >= warm
		rec.Planned = planned.Sub(start).Nanoseconds()
		select {
		case slots <- struct{}{}:
		default:
			// Timed window accounting must exclude discarded warmup arrivals.
			if i >= warm {
				res.Dropped++
			} else {
				res.WarmupDropped++
			}
			continue
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			defer func() { <-slots }()
			rec := &records[i]
			rec.Issued = true
			rec.Sent = time.Since(start).Nanoseconds()
			local, cancel := context.WithTimeout(ctx, c.Timeout)
			clients.do(local, plan[i], i, rec)
			cancel()
			rec.Done = time.Since(start).Nanoseconds()
		}(i)
	}
	// Use the complete arrival interval, including the final inter-arrival slot.
	windowEnd := windowStart.Add(c.Window)
	if d := time.Until(windowEnd); d > 0 {
		select {
		case <-time.After(d):
		case <-ctx.Done():
			wg.Wait()
			return res, records, ctx.Err()
		}
	}
	wg.Wait()
	if c.Admin != "" {
		var snap json.RawMessage
		if raw := fetch(admin, c.Admin+"/debug/latency"); validLatency(raw) {
			snap = raw
		}
		after := counters(admin, c.Admin)
		res.Server["latency"] = snap
		res.Server["counters_before"], res.Server["counters_after"] = before, after
		delta := map[string]float64{}
		for k, v := range after {
			delta[k] = v - before[k]
		}
		res.Server["counters_delta"] = delta
		// Counters include request drain; keep the actual bracket duration explicit.
		secs := time.Since(windowStart).Seconds()
		res.Server["window_seconds_approx"] = secs
		res.Server["source_reads_per_second"] = delta["bcl_e1_source_reads_total"] / secs
		if lookups := delta["bcl_e1_cache_hits_total"] + delta["bcl_e1_cache_misses_total"]; lookups > 0 {
			res.Server["cache_hit_ratio"] = delta["bcl_e1_cache_hits_total"] / lookups
		}
		strategy := fetch(admin, c.Admin+"/debug/strategy")
		if json.Valid(strategy) {
			res.Server["strategy"] = json.RawMessage(strategy)
		}
		telemetryOK = telemetryOK && json.Valid(snap) && json.Valid(strategy) && validCounters(after)
		for _, v := range delta {
			if v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
				telemetryOK = false
			}
		}
	}
	windowStartNS, windowEndNS := windowStart.Sub(start).Nanoseconds(), windowEnd.Sub(start).Nanoseconds()
	within := 0
	for _, r := range records {
		if r.Window && r.Issued && r.Sent >= windowStartNS && r.Sent < windowEndNS {
			within++
		}
	}
	summarizeS1(&res, records, c.Window.Seconds())
	res.ObservedRate = float64(within) / c.Window.Seconds()
	if !telemetryOK {
		res.InvalidReasons = append(res.InvalidReasons, "server telemetry unavailable or counters reset")
	}
	if res.Dropped != 0 {
		res.InvalidReasons = append(res.InvalidReasons, "client dropped scheduled arrivals")
	}
	if res.Reads.Failed+res.Writes.Failed != 0 {
		res.InvalidReasons = append(res.InvalidReasons, "measured requests failed")
	}
	for _, r := range records[:warm] {
		if r.Issued && (r.Err != "" || r.Status != 200) {
			res.InvalidReasons = append(res.InvalidReasons, "warmup requests failed")
			break
		}
	}
	if res.HistogramOverflow != 0 {
		res.InvalidReasons = append(res.InvalidReasons, "histogram overflow")
	}
	if math.Abs(res.ObservedRate/float64(c.Rate)-1) > 0.05 {
		res.InvalidReasons = append(res.InvalidReasons, "arrival rate deviated over five percent")
	}
	res.Valid = ctx.Err() == nil && len(res.InvalidReasons) == 0
	return res, records, nil
}

func validLatency(raw []byte) bool {
	var snap struct {
		Get, Put *hdr.Snapshot
	}
	return json.Unmarshal(raw, &snap) == nil && snap.Get != nil && snap.Put != nil &&
		len(snap.Get.Counts) > 0 && len(snap.Put.Counts) > 0
}

func validCounters(m map[string]float64) bool {
	for _, k := range []string{"bcl_e1_source_reads_total", "bcl_e1_cache_hits_total", "bcl_e1_cache_misses_total"} {
		v, ok := m[k]
		if !ok || v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
			return false
		}
	}
	return true
}

func fetch(c *http.Client, url string) []byte {
	resp, err := c.Get(url)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil || resp.StatusCode != 200 {
		return nil
	}
	return b
}

// counters parses only the bcl_e1_* counter lines of a Prometheus text page.
func counters(c *http.Client, admin string) map[string]float64 {
	out := map[string]float64{}
	sc := bufio.NewScanner(bytes.NewReader(fetch(c, admin+"/metrics")))
	for sc.Scan() {
		name, value, ok := strings.Cut(sc.Text(), " ")
		if ok && strings.HasPrefix(name, "bcl_e1_") {
			if v, err := strconv.ParseFloat(value, 64); err == nil && !math.IsNaN(v) && !math.IsInf(v, 0) {
				out[name] = v
			}
		}
	}
	return out
}

// summarizeS1 computes window statistics. A read is stale when its version is lower than the
// highest version of the same id whose write acknowledgement this process had received
// before the read was sent; writes still in flight are not counted against it. This is a
// conservative (never over-counting) definition, valid because every run starts with an
// empty cache and a restored table, and all writes come from this process.
func summarizeS1(res *S1Result, recs []S1Record, windowSecs float64) {
	readH, writeH, ageH := hdr.New(1, 600_000_000, 3), hdr.New(1, 600_000_000, 3), hdr.New(1, 600_000_000, 3)
	type ack struct{ at, version int64 }
	acks := map[uint32][]ack{}
	for _, r := range recs {
		if r.Write && r.Issued && r.Err == "" && r.Status == 200 {
			acks[r.ID] = append(acks[r.ID], ack{r.Done, r.Version})
		}
	}
	for _, list := range acks {
		sort.Slice(list, func(i, j int) bool { return list[i].at < list[j].at })
	}
	windowSent := 0
	for _, r := range recs {
		if !r.Window || !r.Issued {
			continue
		}
		windowSent++
		ok := r.Err == "" && r.Status == 200
		lat := max(1, (r.Done-r.Planned)/1000) // planned arrival → completion, open-loop
		stats, h := &res.Reads, readH
		if r.Write {
			stats, h = &res.Writes, writeH
		}
		stats.Count++
		if ok {
			stats.Success++
		} else {
			stats.Failed++
		}
		if h.RecordValue(lat) != nil {
			res.HistogramOverflow++
		}
		if r.Write || !ok {
			continue
		}
		if strings.HasPrefix(r.Source, "cache") {
			res.ClientCacheServed++
		}
		list := acks[r.ID]
		if len(list) == 0 {
			continue
		}
		res.ReadsOfWrittenIDs++
		// Highest acknowledged version before this read was sent, and when it was acknowledged.
		var best ack
		for _, a := range list {
			if a.at >= r.Sent {
				break
			}
			if a.version > best.version {
				best = a
			}
		}
		if best.version > 0 && r.Version < best.version {
			res.StaleReads++
			ageH.RecordValue(max(1, (r.Sent-best.at)/1000))
		}
	}
	res.ObservedRate = float64(windowSent) / windowSecs
	res.Reads.Latency, res.Reads.Histogram = summary(readH), readH.Export()
	res.Writes.Latency, res.Writes.Histogram = summary(writeH), writeH.Export()
	res.StaleAge = summary(ageH)
	if res.Reads.Success > 0 {
		res.StaleRatio = float64(res.StaleReads) / float64(res.Reads.Success)
	}
	res.Definitions = []string{
		"latency: planned arrival to completion (open loop), microseconds",
		"dropped: measured-window arrivals not issued; warmup_dropped is reported separately and excluded",
		"stale read: version below the highest same-id write acknowledged to this process before the read was sent",
		"stale_ratio: stale reads / successful window reads",
		"stale_age: read send time minus acknowledgement time of the newer version",
		"server cache_hit_ratio: hits / (hits + misses) over the window; strict mode looks up the cache on every read",
	}
}

// WriteS1Raw writes every record as gzip CSV for audit and re-analysis.
func WriteS1Raw(path string, recs []S1Record) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	gz := gzip.NewWriter(f)
	w := bufio.NewWriter(gz)
	fmt.Fprintln(w, "window,write,id,planned_ns,sent_ns,done_ns,sent,status,version,source,err")
	b := func(v bool) int {
		if v {
			return 1
		}
		return 0
	}
	for _, r := range recs {
		fmt.Fprintf(w, "%d,%d,%d,%d,%d,%d,%d,%d,%d,%s,%s\n", b(r.Window), b(r.Write), r.ID, r.Planned, r.Sent, r.Done, b(r.Issued), r.Status, r.Version, r.Source, r.Err)
	}
	return errors.Join(w.Flush(), gz.Close(), f.Close())
}
