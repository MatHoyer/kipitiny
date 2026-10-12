package api

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"

	"github.com/MatHoyer/kipitiny/internal/core"
)

// test answers a call checking an integration still works: 204 or the
// error, or with ?progress its steps as they start (see progress).
func (a *API) test(w http.ResponseWriter, r *http.Request, run func(context.Context) error) {
	if r.URL.Query().Has("progress") {
		a.progress(w, r, func(ctx context.Context) (any, error) { return nil, run(ctx) })
		return
	}
	if err := run(r.Context()); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// progress runs op, streaming each step it reports as a line of NDJSON
// ({"step":"login"}), then its outcome: {"result":…} or {"error":…}. The
// audit log records the status a plain call would have answered.
func (a *API) progress(w http.ResponseWriter, r *http.Request, op func(context.Context) (any, error)) {
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)
	enc := json.NewEncoder(w)
	var mu sync.Mutex
	send := func(v any) {
		mu.Lock()
		defer mu.Unlock()
		_ = enc.Encode(v)
		_ = rc.Flush()
	}
	ctx := core.WithProgress(r.Context(), func(step string) { send(map[string]string{"step": step}) })
	v, err := op(ctx)
	if err != nil {
		code, msg := a.failure(err)
		if rec, ok := w.(*statusRecorder); ok {
			rec.status = code
		}
		send(map[string]string{"error": msg})
		return
	}
	send(map[string]any{"result": v})
}
