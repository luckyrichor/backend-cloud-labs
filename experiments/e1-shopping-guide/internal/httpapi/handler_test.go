package httpapi_test

import (
	"encoding/json"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/cache"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/catalogtest"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/guide"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/httpapi"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPShoppingLoopAndValidation(t *testing.T) {
	h := httpapi.Handler(guide.Service{Store: catalogtest.NewStore(t), Cache: cache.NewMemory(time.Minute)})
	cases := []struct {
		method, path, body string
		code               int
		contains           string
	}{
		{"GET", "/items/998", "", 404, "Not Found"},
		{"PUT", "/items/1", `{"title":"kettle","price_cent":100,"stock":1}`, 200, `"version":1`},
		{"GET", "/items/1", "", 200, "cache_validated"},
		{"POST", "/recommendations", `{"ids":["1"],"budget_cent":100}`, 200, "kettle"},
		{"PUT", "/items/1", `{"title":"a","price_cent":-1}`, 400, "invalid item"},
		{"PUT", "/items/1", `{"title":"a","version":999}`, 400, "invalid JSON"},
		{"POST", "/recommendations", `{"ids":[],"budget_cent":100}`, 400, "invalid query"},
		{"PUT", "/items/1", `{"title":"a"} {}`, 400, "invalid JSON"},
	}
	for _, c := range cases {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(c.method, c.path, strings.NewReader(c.body)))
		if w.Code != c.code || !strings.Contains(w.Body.String(), c.contains) {
			t.Fatalf("%s: %d %s", c.path, w.Code, w.Body.String())
		}
	}
}

func TestItemResponseHasOnlySnakeCaseFields(t *testing.T) {
	h := httpapi.Handler(guide.Service{Store: catalogtest.NewStore(t), Cache: cache.NewMemory(time.Minute)})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("PUT", "/items/101", strings.NewReader(`{"title":"kettle","price_cent":100,"stock":2}`)))
	var data map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	var item map[string]json.RawMessage
	if err := json.Unmarshal(data["item"], &item); err != nil {
		t.Fatal(err)
	}
	want := []string{"id", "title", "price_cent", "stock", "version", "updated_at"}
	if len(item) != len(want) {
		t.Fatal(item)
	}
	for _, key := range want {
		if _, ok := item[key]; !ok {
			t.Fatal("missing", key, item)
		}
	}
}

func TestWriteTokenGuardsOnlyWrites(t *testing.T) {
	h := httpapi.HandlerWithOptions(guide.Service{Store: catalogtest.NewStore(t), Cache: cache.NewMemory(time.Minute)}, httpapi.Options{WriteToken: "s3cret"})
	body := `{"title":"kettle","price_cent":100,"stock":1}`
	for _, c := range []struct {
		method, auth string
		code         int
	}{
		{"PUT", "", 401}, {"PUT", "Bearer wrong", 401}, {"PUT", "s3cret", 401}, {"PUT", "Bearer s3cret", 200},
		{"GET", "", 200},
	} {
		r := httptest.NewRequest(c.method, "/items/1", strings.NewReader(body))
		if c.method == "GET" {
			r = httptest.NewRequest("GET", "/items/1", nil)
		}
		if c.auth != "" {
			r.Header.Set("Authorization", c.auth)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != c.code {
			t.Fatalf("%s auth=%q: %d, want %d", c.method, c.auth, w.Code, c.code)
		}
		if c.code == 401 && w.Header().Get("WWW-Authenticate") != "Bearer" {
			t.Fatal("401 without WWW-Authenticate")
		}
	}
	// A rejected write must not have reached the source: the first accepted write is version 1.
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/items/1", nil))
	if !strings.Contains(w.Body.String(), `"version":1`) {
		t.Fatalf("rejected writes changed state: %s", w.Body.String())
	}
}
