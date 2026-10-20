// Package httpdemo exposes the existing C++ evaluator, not a replacement implementation.
package httpdemo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Request struct {
	Expression string             `json:"expression"`
	Variables  map[string]float64 `json:"variables"`
}

func Handler(binary string) http.Handler {
	mux := http.NewServeMux()
	slots := make(chan struct{}, 4)
	mux.HandleFunc("POST /evaluate", func(w http.ResponseWriter, r *http.Request) {
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			http.Error(w, "capacity", 429)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1048576)
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		var in Request
		if decoder.Decode(&in) != nil || decoder.Decode(new(any)) != io.EOF || in.Expression == "" || len(in.Expression) > 4096 || len(in.Variables) > 256 {
			http.Error(w, "invalid_request", 400)
			return
		}
		keys := make([]string, 0, len(in.Variables))
		for key, value := range in.Variables {
			if len(key) > 4096 || math.IsNaN(value) || math.IsInf(value, 0) {
				http.Error(w, "invalid_request", 400)
				return
			}
			keys = append(keys, key)
		}
		sort.Strings(keys)
		args := []string{in.Expression}
		for _, key := range keys {
			args = append(args, key, strconv.FormatFloat(in.Variables[key], 'g', -1, 64))
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, binary, args...).Output()
		if err != nil {
			var exit *exec.ExitError
			if errors.As(err, &exit) && exit.ExitCode() == 2 {
				http.Error(w, "invalid_expression", 400)
			} else {
				http.Error(w, "evaluator_unavailable", 503)
			}
			return
		}
		var value float64
		var ns int64
		if _, err = fmt.Fscan(strings.NewReader(string(out)), &value, &ns); err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
			http.Error(w, "invalid_evaluator_output", 503)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"value": value, "parse_and_evaluate_ns": ns, "measurement_scope": "single-process demonstration; not S4/S5 benchmark"})
	})
	return mux
}
