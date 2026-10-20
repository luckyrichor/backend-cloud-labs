package loadgen

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenLoopIncludesServerPause(t *testing.T) {
	pauseStart := time.Now().Add(200 * time.Millisecond)
	pauseEnd := pauseStart.Add(time.Second)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if now := time.Now(); now.After(pauseStart) && now.Before(pauseEnd) {
			time.Sleep(time.Until(pauseEnd))
		}
		w.WriteHeader(204)
	}))
	defer server.Close()
	result, err := Run(context.Background(), Config{Target: server.URL, Protocol: "http", Rate: 100, Duration: 2 * time.Second, Timeout: 3 * time.Second, MaxInflight: 256})
	if err != nil || !result.Valid || result.Check() != nil || result.Sent != 200 || result.Failed != 0 {
		t.Fatalf("%+v %v", result, err)
	}
	if result.ResponseLatency.P99 < 800000 {
		t.Fatalf("one-second pause hidden: p99=%d", result.ResponseLatency.P99)
	}
	if result.SendRateDeviation < -0.05 || result.SendRateDeviation > 0.05 {
		t.Fatalf("arrival rate mismatch %+v", result)
	}
}
func TestDirtyRepositoryRefused(t *testing.T) {
	repo := t.TempDir()
	cmd := exec.Command("git", "init", repo)
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "dirty"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := RequireClean(repo); err == nil {
		t.Fatal("dirty repository accepted")
	}
}
func TestBoundedClientReportsOverload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { time.Sleep(50 * time.Millisecond) }))
	defer server.Close()
	r, err := Run(context.Background(), Config{Target: server.URL, Protocol: "http", Rate: 2000, Duration: 100 * time.Millisecond, Timeout: time.Second, MaxInflight: 1})
	if err != nil || r.Dropped == 0 || r.Valid || r.Check() != nil {
		t.Fatalf("%+v %v", r, err)
	}
}
