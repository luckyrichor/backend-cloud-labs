package telemetry

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestConcurrentMetricsAndAdminIsolation(t *testing.T) {
	m := &Metrics{}
	app := m.HTTP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) }))
	var group sync.WaitGroup
	for i := 0; i < 50; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			app.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/metrics?secret=unused", nil))
			m.Frame(time.Millisecond)
			m.Bytes(10)
		}()
	}
	group.Wait()
	recorder := httptest.NewRecorder()
	m.Handler().ServeHTTP(recorder, httptest.NewRequest("GET", "/metrics", nil))
	body := recorder.Body.String()
	for _, wanted := range []string{"bcl_http_request_duration_seconds_count 50", "bcl_room_frame_duration_seconds_count 50", "bcl_room_sent_bytes_total 500", "_bucket{le=\"+Inf\"} 50"} {
		if !strings.Contains(body, wanted) {
			t.Fatal(wanted, body)
		}
	}
	if strings.Contains(body, "secret") {
		t.Fatal("unbounded request content in metrics")
	}
	recorder = httptest.NewRecorder()
	app.ServeHTTP(recorder, httptest.NewRequest("GET", "/debug/pprof/", nil))
	if recorder.Code != 404 {
		t.Fatal("pprof exposed on application handler")
	}
}
func TestConfigRejectsInvalidLimits(t *testing.T) {
	for _, v := range []string{"0", "-1", "x"} {
		t.Setenv("TEST_LIMIT", v)
		if _, err := PositiveInt("TEST_LIMIT", 16); err == nil {
			t.Fatal(v)
		}
	}
}

func TestCountersAndRoutesAppearOnAdminHandler(t *testing.T) {
	m := &Metrics{}
	hits := m.Counter("bcl_test_hits_total")
	hits.Add(3)
	m.Route("GET /debug/extra", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("extra")) }))
	h := m.Handler()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
	if !strings.Contains(w.Body.String(), "bcl_test_hits_total 3\n") {
		t.Fatalf("counter missing: %s", w.Body.String())
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/debug/extra", nil))
	if w.Body.String() != "extra" {
		t.Fatalf("route missing: %q", w.Body.String())
	}
}
