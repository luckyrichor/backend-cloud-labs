// Package loadgen schedules arrivals independently of completion to expose queueing delay.
package loadgen

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	hdr "github.com/HdrHistogram/hdrhistogram-go"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type Config struct {
	Target, Protocol, Method string
	Body                     []byte
	Rate                     int
	Duration, Timeout        time.Duration
	MaxInflight              int
	Authorization            string
}
type Summary struct {
	Count int64 `json:"count"`
	P50   int64 `json:"p50_us"`
	P95   int64 `json:"p95_us"`
	P99   int64 `json:"p99_us"`
	Max   int64 `json:"max_us"`
}
type Result struct {
	StartedAt                                                    time.Time `json:"started_at"`
	Scheduled, Sent, Success, Failed, Dropped, HistogramOverflow int
	DurationSeconds, ObservedSendRate, SendRateDeviation         float64
	SchedulingLag, ResponseLatency, DispatchToComplete           Summary
	ResponseHistogram, SchedulingHistogram                       *hdr.Snapshot
	Valid                                                        bool
	Unit, Scope, Protocol                                        string
}
type observation struct {
	success               bool
	sent                  bool
	latency, lag, service time.Duration
	dispatched            time.Time
}

func RequireClean(repo string) (string, error) {
	cmd := exec.Command("git", "-C", repo, "status", "--porcelain")
	out, err := cmd.Output()
	if err != nil || len(bytes.TrimSpace(out)) != 0 {
		return "", errors.New("clean Git worktree required")
	}
	out, err = exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	return strings.TrimSpace(string(out)), err
}
func summary(h *hdr.Histogram) Summary {
	return Summary{h.TotalCount(), h.ValueAtQuantile(50), h.ValueAtQuantile(95), h.ValueAtQuantile(99), h.Max()}
}
func measured(h *hdr.Histogram, d time.Duration) bool {
	n := d.Microseconds()
	if n < 1 {
		n = 1
	}
	return h.RecordValue(n) == nil
}
func Run(ctx context.Context, c Config) (Result, error) {
	if c.Rate <= 0 || c.Rate > 100000 || c.Duration <= 0 || c.Timeout <= 0 || c.MaxInflight <= 0 || c.MaxInflight > 100000 {
		return Result{}, errors.New("positive bounded rate, duration, timeout and concurrency required")
	}
	if c.Protocol != "http" && c.Protocol != "tcp" {
		return Result{}, errors.New("protocol must be http or tcp")
	}
	if c.Protocol == "http" {
		u, err := url.Parse(c.Target)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
			return Result{}, errors.New("invalid HTTP target")
		}
	}
	n := int(float64(c.Rate) * c.Duration.Seconds())
	if n < 1 || n > 10000000 {
		return Result{}, errors.New("scheduled count outside [1,10000000]")
	}
	result := Result{Scheduled: n, DurationSeconds: c.Duration.Seconds(), Unit: "microseconds; minimum1, maximum600000000, significant digits3", Scope: "open-loop; response latency includes scheduled-arrival-to-completion, not only transport time", Protocol: c.Protocol}
	latency := hdr.New(1, 600000000, 3)
	lag := hdr.New(1, 600000000, 3)
	service := hdr.New(1, 600000000, 3)
	observations := make(chan observation, c.MaxInflight+1)
	slots := make(chan struct{}, c.MaxInflight)
	transport := &http.Transport{MaxIdleConns: c.MaxInflight, MaxIdleConnsPerHost: c.MaxInflight, IdleConnTimeout: time.Minute}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	start := time.Now()
	result.StartedAt = start.UTC()
	windowEnd := start.Add(c.Duration)
	var group sync.WaitGroup
	go func() {
		defer close(observations)
		for i := 0; i < n; i++ {
			planned := start.Add(time.Duration(float64(i) * float64(time.Second) / float64(c.Rate)))
			if delay := time.Until(planned); delay > 0 {
				timer := time.NewTimer(delay)
				select {
				case <-timer.C:
				case <-ctx.Done():
					timer.Stop()
					group.Wait()
					return
				}
			}
			select {
			case slots <- struct{}{}:
				group.Add(1)
				go func(planned time.Time) {
					defer group.Done()
					defer func() { <-slots }()
					actual := time.Now()
					local, cancel := context.WithTimeout(ctx, c.Timeout)
					defer cancel()
					ok := false
					if c.Protocol == "http" {
						method := c.Method
						if method == "" {
							method = "GET"
						}
						req, err := http.NewRequestWithContext(local, method, c.Target, bytes.NewReader(c.Body))
						if err == nil {
							if c.Authorization != "" {
								req.Header.Set("Authorization", c.Authorization)
							}
							if len(c.Body) > 0 {
								req.Header.Set("Content-Type", "application/json")
							}
							response, err := client.Do(req)
							if err == nil {
								received, readErr := io.Copy(io.Discard, io.LimitReader(response.Body, 1048577))
								_ = response.Body.Close()
								ok = received <= 1048576 && readErr == nil && response.StatusCode >= 200 && response.StatusCode < 300
							}
						}
					} else {
						dialer := net.Dialer{}
						conn, err := dialer.DialContext(local, "tcp", c.Target)
						if err == nil {
							deadline, _ := local.Deadline()
							_ = conn.SetDeadline(deadline)
							body := bytes.TrimSuffix(c.Body, []byte{'\n'})
							body = append(append([]byte(nil), body...), '\n')
							_, err = conn.Write(body)
							if err == nil {
								reader := bufio.NewReader(io.LimitReader(conn, 65537))
								line, err := reader.ReadBytes('\n')
								var reply map[string]any
								ok = err == nil && len(line) <= 65536 && json.Unmarshal(line, &reply) == nil && reply["type"] != "error"
							}
							_ = conn.Close()
						}
					}
					ended := time.Now()
					observations <- observation{ok, true, ended.Sub(planned), actual.Sub(planned), ended.Sub(actual), actual}
				}(planned)
			default:
				observations <- observation{sent: false}
			}
		}
		group.Wait()
	}()
	within := 0
	for o := range observations {
		if !o.sent {
			result.Dropped++
			continue
		}
		result.Sent++
		if o.success {
			result.Success++
		} else {
			result.Failed++
		}
		if o.dispatched.Before(windowEnd) {
			within++
		}
		if !measured(latency, o.latency) || !measured(lag, o.lag) || !measured(service, o.service) {
			result.HistogramOverflow++
		}
	}
	result.ObservedSendRate = float64(within) / c.Duration.Seconds()
	result.SendRateDeviation = (result.ObservedSendRate - float64(c.Rate)) / float64(c.Rate)
	result.SchedulingLag = summary(lag)
	result.ResponseLatency = summary(latency)
	result.DispatchToComplete = summary(service)
	result.ResponseHistogram = latency.Export()
	result.SchedulingHistogram = lag.Export()
	result.Valid = ctx.Err() == nil && result.Dropped == 0 && result.HistogramOverflow == 0 && result.Sent == result.Scheduled
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	return result, nil
}
func (r Result) Check() error {
	if r.Success+r.Failed != r.Sent || r.Sent+r.Dropped != r.Scheduled {
		return fmt.Errorf("request accounting mismatch")
	}
	return nil
}
