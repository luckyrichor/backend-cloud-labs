package cmdb

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
)

func Handler(s *Store) http.Handler {
	mux := http.NewServeMux()
	write := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("POST /changes", func(w http.ResponseWriter, r *http.Request) {
		var c Change
		d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536))
		d.DisallowUnknownFields()
		if err := d.Decode(&c); err != nil {
			http.Error(w, "invalid JSON", 400)
			return
		}
		if d.Decode(new(any)) != io.EOF {
			http.Error(w, "trailing JSON", 400)
			return
		}
		state, err := s.Apply(c)
		if err != nil {
			code := 400
			if errors.Is(err, ErrConflict) {
				code = 409
			}
			http.Error(w, err.Error(), code)
			return
		}
		write(w, state)
	})
	mux.HandleFunc("GET /state", func(w http.ResponseWriter, r *http.Request) {
		state := s.Current()
		if text := r.URL.Query().Get("revision"); text != "" {
			rev, err := strconv.ParseUint(text, 10, 64)
			if err != nil {
				http.Error(w, "revision", 400)
				return
			}
			state, err = s.Snapshot(rev)
			if err != nil {
				http.Error(w, err.Error(), 404)
				return
			}
		}
		write(w, state)
	})
	mux.HandleFunc("GET /graph", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		depth, err := strconv.Atoi(q.Get("depth"))
		if err != nil {
			http.Error(w, "depth required", 400)
			return
		}
		state := s.Current()
		if text := q.Get("revision"); text != "" {
			rev, e := strconv.ParseUint(text, 10, 64)
			if e != nil {
				http.Error(w, "revision", 400)
				return
			}
			state, err = s.Snapshot(rev)
			if err != nil {
				http.Error(w, err.Error(), 404)
				return
			}
		}
		g, err := GraphAt(state, q.Get("root"), depth, q.Get("direction"))
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		write(w, g)
	})
	mux.HandleFunc("GET /audit", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		offset, limit := 0, 20
		var err error
		if q.Has("offset") {
			offset, err = strconv.Atoi(q.Get("offset"))
			if err != nil {
				http.Error(w, "offset", 400)
				return
			}
		}
		if q.Has("limit") {
			limit, err = strconv.Atoi(q.Get("limit"))
			if err != nil {
				http.Error(w, "limit", 400)
				return
			}
		}
		a, err := s.Audits(offset, limit)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		write(w, a)
	})
	return mux
}
