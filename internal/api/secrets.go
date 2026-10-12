package api

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"

	"github.com/MatHoyer/kipitiny/internal/core"
)

func (a *API) secretProviders(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.core.SecretProviders(r.Context()))
}

func (a *API) connectSecretProvider(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string `json:"token"`
	}
	if !decode(w, r, &body) {
		return
	}
	connect := func(ctx context.Context) (any, error) {
		return a.core.ConnectSecretProvider(ctx, r.PathValue("id"), body.Token)
	}
	if r.URL.Query().Has("progress") {
		a.progress(w, r, connect)
		return
	}
	v, err := connect(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (a *API) disconnectSecretProvider(w http.ResponseWriter, r *http.Request) {
	if err := a.core.DisconnectSecretProvider(r.Context(), r.PathValue("id")); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) secretVaults(w http.ResponseWriter, r *http.Request) {
	vs, err := a.core.SecretVaults(r.Context(), r.PathValue("id"), r.URL.Query().Has("refresh"))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, vs)
}

func (a *API) secretItems(w http.ResponseWriter, r *http.Request) {
	items, err := a.core.SecretItems(r.Context(), r.PathValue("id"), r.URL.Query().Get("vault"), r.URL.Query().Has("refresh"))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (a *API) testSecretProvider(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Has("progress") {
		a.progress(w, r, func(ctx context.Context) (any, error) {
			return nil, a.core.TestSecretProvider(ctx, r.PathValue("id"))
		})
		return
	}
	if err := a.core.TestSecretProvider(r.Context(), r.PathValue("id")); err != nil {
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
	ctx := core.WithSecretProgress(r.Context(), func(step string) { send(map[string]string{"step": step}) })
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
