// Package audit records all-key read and database-commit-return events without
// mixing process-relative clocks. Files carry Linux boot ID and explicit phases.
package audit

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
)

type labels struct{ Run, Phase, Request string }
type labelKey struct{}

var labelPattern = regexp.MustCompile(`^[a-zA-Z0-9_.:-]{1,128}$`)

func clean(s string) string {
	if labelPattern.MatchString(s) {
		return s
	}
	return ""
}
func WithLabels(ctx context.Context, run, phase, request string) context.Context {
	return context.WithValue(ctx, labelKey{}, labels{clean(run), clean(phase), clean(request)})
}
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := WithLabels(r.Context(), r.Header.Get("X-BCL-Run"), r.Header.Get("X-BCL-Phase"), r.Header.Get("X-BCL-Request"))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

type Event struct {
	Schema   int    `json:"schema"`
	Boot     string `json:"boot_id"`
	Instance string `json:"instance"`
	Op       uint64 `json:"op"`
	ID       string `json:"id,omitempty"`
	Event    string `json:"event"`
	Version  int64  `json:"version,omitempty"`
	TNS      int64  `json:"t_ns"`
	Run      string `json:"run,omitempty"`
	Phase    string `json:"phase,omitempty"`
	Request  string `json:"request,omitempty"`
}
type Writer struct {
	mu             sync.Mutex
	next           atomic.Uint64
	f              *os.File
	b              *bufio.Writer
	boot, instance string
	err            error
	count          uint64
	closed         bool
}

func Open(path, instance string) (*Writer, error) {
	if runtime.GOOS != "linux" {
		return nil, errors.New("audit requires Linux host-wide CLOCK_MONOTONIC")
	}
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return nil, err
	}
	if clean(instance) == "" {
		return nil, errors.New("audit instance label required")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	w := &Writer{f: f, b: bufio.NewWriterSize(f, 256*1024), boot: strings.TrimSpace(string(boot)), instance: instance}
	w.RecordAt(context.Background(), "", "start", 0, 0, Now())
	return w, nil
}
func (w *Writer) Begin(ctx context.Context, id string) uint64 {
	op := w.next.Add(1)
	w.RecordAt(ctx, id, "read_begin", op, 0, Now())
	return op
}
func (w *Writer) End(ctx context.Context, id string, op uint64, version int64, err error) {
	name := "read_return"
	if err != nil {
		name = "read_error"
	}
	w.RecordAt(ctx, id, name, op, version, Now())
}
func (w *Writer) RecordAt(ctx context.Context, id, name string, op uint64, version, t int64) {
	l, _ := ctx.Value(labelKey{}).(labels)
	e := Event{Schema: 1, Boot: w.boot, Instance: w.instance, Op: op, ID: id, Event: name, Version: version, TNS: t, Run: l.Run, Phase: l.Phase, Request: l.Request}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		if w.err == nil {
			w.err = errors.New("audit write after close")
		}
		return
	}
	if w.err == nil {
		w.err = json.NewEncoder(w.b).Encode(e)
		if w.err == nil {
			w.count++
		}
	}
}
func (w *Writer) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.closed && w.err == nil {
		w.err = w.b.Flush()
	}
	return w.err
}
func (w *Writer) Status() (uint64, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.count, w.err != nil
}
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return w.err
	}
	w.err = errors.Join(w.err, w.b.Flush(), w.f.Sync(), w.f.Close())
	w.closed = true
	return w.err
}
