package httpapi

import (
	"encoding/json"
	"errors"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/catalog"
	"github.com/luckyrichor/backend-cloud-labs/experiments/e1-shopping-guide/internal/guide"
	"io"
	"net/http"
)

func Handler(service guide.Service) http.Handler {
	if service.Repairs == nil {
		service.Repairs = guide.NewRepairQueue(1024)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /items/{id}", func(w http.ResponseWriter, r *http.Request) {
		result, err := service.Get(r.Context(), r.PathValue("id"))
		respond(w, result, err)
	})
	mux.HandleFunc("PUT /items/{id}", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Title     string `json:"title"`
			PriceCent int64  `json:"price_cent"`
			Stock     int    `json:"stock"`
		}
		if !decode(w, r, &body) {
			return
		}
		if body.Title == "" || body.PriceCent < 0 || body.Stock < 0 {
			http.Error(w, "invalid item", 400)
			return
		}
		result, err := service.Put(r.Context(), catalog.Item{ID: r.PathValue("id"), Title: body.Title,
			PriceCent: body.PriceCent, Stock: body.Stock})
		respond(w, result, err)
	})
	mux.HandleFunc("POST /recommendations", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			IDs        []string `json:"ids"`
			BudgetCent int64    `json:"budget_cent"`
		}
		if !decode(w, r, &body) {
			return
		}
		if len(body.IDs) == 0 || len(body.IDs) > 100 || body.BudgetCent < 0 {
			http.Error(w, "invalid query", 400)
			return
		}
		result, err := service.Recommend(r.Context(), body.IDs, body.BudgetCent)
		respond(w, result, err)
	})
	return mux
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if decoder.Decode(v) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		http.Error(w, "invalid JSON", 400)
		return false
	}
	return true
}
func respond(w http.ResponseWriter, v any, err error) {
	if err != nil {
		code := 503
		if errors.Is(err, catalog.ErrNotFound) {
			code = 404
		}
		http.Error(w, http.StatusText(code), code)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
