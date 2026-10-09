package httpapi_test

import (
	"encoding/json"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/cache"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/catalog"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/guide"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/httpapi"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPShoppingLoopAndValidation(t *testing.T) {
	h := httpapi.Handler(guide.Service{Store: catalog.NewMemoryStore(), Cache: cache.NewMemory(time.Minute)})
	cases := []struct {
		method, path, body string
		code               int
		contains           string
	}{
		{"GET", "/items/no", "", 404, "Not Found"},
		{"PUT", "/items/a", `{"title":"kettle","price_cent":100,"stock":1}`, 200, `"version":1`},
		{"GET", "/items/a", "", 200, "cache_validated"},
		{"POST", "/recommendations", `{"ids":["a"],"budget_cent":100}`, 200, "kettle"},
		{"PUT", "/items/a", `{"title":"a","price_cent":-1}`, 400, "invalid item"},
		{"PUT", "/items/a", `{"title":"a","version":999}`, 400, "invalid JSON"},
		{"POST", "/recommendations", `{"ids":[],"budget_cent":100}`, 400, "invalid query"},
		{"PUT", "/items/a", `{"title":"a"} {}`, 400, "invalid JSON"},
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
	h := httpapi.Handler(guide.Service{Store: catalog.NewMemoryStore(), Cache: cache.NewMemory(time.Minute)})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("PUT", "/items/sku", strings.NewReader(`{"title":"kettle","price_cent":100,"stock":2}`)))
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
