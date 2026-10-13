package api

import (
	"context"
	"net/http"
)

func (a *API) secretProviders(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.core.SecretProviders())
}

func (a *API) secretConnections(w http.ResponseWriter, r *http.Request) {
	cs, err := a.core.SecretConnections(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, cs)
}

// connectSecret runs connect, streaming its steps when ?progress is set:
// the first login may pull the password manager's CLI.
func (a *API) connectSecret(w http.ResponseWriter, r *http.Request, status int, connect func(context.Context) (any, error)) {
	if r.URL.Query().Has("progress") {
		a.progress(w, r, connect)
		return
	}
	v, err := connect(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, status, v)
}

func (a *API) createSecretConnection(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Kind  string `json:"kind"`
		Name  string `json:"name"`
		Token string `json:"token"`
	}
	if !decode(w, r, &body) {
		return
	}
	a.connectSecret(w, r, http.StatusCreated, func(ctx context.Context) (any, error) {
		return a.core.CreateSecretConnection(ctx, body.Kind, body.Name, body.Token)
	})
}

func (a *API) updateSecretConnection(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string `json:"token"`
	}
	if !decode(w, r, &body) {
		return
	}
	a.connectSecret(w, r, http.StatusOK, func(ctx context.Context) (any, error) {
		return a.core.UpdateSecretConnection(ctx, r.PathValue("id"), body.Token)
	})
}

func (a *API) deleteSecretConnection(w http.ResponseWriter, r *http.Request) {
	if err := a.core.DeleteSecretConnection(r.Context(), r.PathValue("id")); err != nil {
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

func (a *API) testSecretConnection(w http.ResponseWriter, r *http.Request) {
	a.test(w, r, func(ctx context.Context) error { return a.core.TestSecretConnection(ctx, r.PathValue("id")) })
}
