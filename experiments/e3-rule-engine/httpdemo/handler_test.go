package httpdemo

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestInvalidRequestNeverStartsEvaluator(t *testing.T) {
	for _, body := range []string{`{"expression":"1","extra":1}`, `{"expression":"1"} {}`, `{"variables":{}}`} {
		r := httptest.NewRecorder()
		Handler("/does-not-exist").ServeHTTP(r, httptest.NewRequest("POST", "/evaluate", strings.NewReader(body)))
		if r.Code != 400 {
			t.Fatalf("%d %s", r.Code, body)
		}
	}
}
func TestMissingBinaryIsUnavailable(t *testing.T) {
	r := httptest.NewRecorder()
	Handler("/does-not-exist").ServeHTTP(r, httptest.NewRequest("POST", "/evaluate", strings.NewReader(`{"expression":"1"}`)))
	if r.Code != 503 {
		t.Fatal(r.Code)
	}
}
