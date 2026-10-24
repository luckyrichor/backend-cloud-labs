// Package telemetry provides bounded-label Prometheus histograms and a separate admin listener.
package telemetry

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

var buckets = [...]float64{0.0001, 0.0005, 0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5}

type histogram struct {
	count   uint64
	sum     float64
	buckets [10]uint64
}
type Metrics struct {
	mu          sync.Mutex
	http, frame histogram
	sent        uint64
	counters    []namedCounter
	routes      []route
}

type namedCounter struct {
	name  string
	value *atomic.Uint64
}
type route struct {
	pattern string
	handler http.Handler
}

// Counter registers a monotonically increasing counter exported on /metrics. Names are
// fixed by code (no user-derived labels); register before Start.
func (m *Metrics) Counter(name string) *atomic.Uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := namedCounter{name, &atomic.Uint64{}}
	m.counters = append(m.counters, c)
	return c.value
}

// Route adds an extra handler to the admin listener (never the business port); register before Start.
func (m *Metrics) Route(pattern string, h http.Handler) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.routes = append(m.routes, route{pattern, h})
}

func (m *Metrics) observe(h *histogram, duration time.Duration) {
	seconds := duration.Seconds()
	h.count++
	h.sum += seconds
	for i, b := range buckets {
		if seconds <= b {
			h.buckets[i]++
		}
	}
}
func (m *Metrics) Frame(d time.Duration) { m.mu.Lock(); defer m.mu.Unlock(); m.observe(&m.frame, d) }
func (m *Metrics) Bytes(n int) {
	if n > 0 {
		m.mu.Lock()
		m.sent += uint64(n)
		m.mu.Unlock()
	}
}
func (m *Metrics) HTTP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		defer func() { m.mu.Lock(); defer m.mu.Unlock(); m.observe(&m.http, time.Since(start)) }()
		next.ServeHTTP(w, r)
	})
}
func writeHistogram(w io.Writer, name string, h histogram) {
	fmt.Fprintf(w, "# TYPE %s histogram\n", name)
	for i, b := range buckets {
		fmt.Fprintf(w, "%s_bucket{le=%q} %d\n", name, strconv.FormatFloat(b, 'g', -1, 64), h.buckets[i])
	}
	fmt.Fprintf(w, "%s_bucket{le=\"+Inf\"} %d\n%s_sum %g\n%s_count %d\n", name, h.count, name, h.sum, name, h.count)
}
func (m *Metrics) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200); _, _ = w.Write([]byte("ok\n")) })
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		h, f, s, counters := m.http, m.frame, m.sent, m.counters
		m.mu.Unlock()
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		writeHistogram(w, "bcl_http_request_duration_seconds", h)
		writeHistogram(w, "bcl_room_frame_duration_seconds", f)
		fmt.Fprintf(w, "# TYPE bcl_room_sent_bytes_total counter\nbcl_room_sent_bytes_total %d\n", s)
		for _, c := range counters {
			fmt.Fprintf(w, "# TYPE %s counter\n%s %d\n", c.name, c.name, c.value.Load())
		}
	})
	m.mu.Lock()
	for _, r := range m.routes {
		mux.Handle(r.pattern, r.handler)
	}
	m.mu.Unlock()
	mux.HandleFunc("GET /debug/pprof/", pprof.Index)
	mux.HandleFunc("GET /debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("GET /debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("GET /debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("GET /debug/pprof/trace", pprof.Trace)
	return mux
}
func Address(key, fallback string) string {
	if s := os.Getenv(key); s != "" {
		return s
	}
	return fallback
}
func PositiveInt(key string, fallback int) (int, error) {
	text := os.Getenv(key)
	if text == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(text)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", key)
	}
	return n, nil
}
func Start(address string, m *Metrics) (*http.Server, error) {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, err
	}
	server := &http.Server{Handler: m.Handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = server.Serve(listener) }()
	return server, nil
}

// ExitHealthcheck is used by container HEALTHCHECK, before application flag parsing.
func ExitHealthcheck(address string) {
	if len(os.Args) != 2 || os.Args[1] != "healthcheck" {
		return
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		os.Exit(1)
	}
	if host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	client := http.Client{Timeout: 2 * time.Second}
	response, err := client.Get("http://" + net.JoinHostPort(host, port) + "/healthz")
	if err != nil {
		os.Exit(1)
	}
	_ = response.Body.Close()
	if response.StatusCode != 200 {
		os.Exit(1)
	}
	os.Exit(0)
}
