package api

import "net/http"

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
	v, err := a.core.ConnectSecretProvider(r.Context(), r.PathValue("id"), body.Token)
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
	vs, err := a.core.SecretVaults(r.Context(), r.PathValue("id"))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, vs)
}

func (a *API) secretItems(w http.ResponseWriter, r *http.Request) {
	items, err := a.core.SecretItems(r.Context(), r.PathValue("id"), r.URL.Query().Get("vault"))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (a *API) testSecretProvider(w http.ResponseWriter, r *http.Request) {
	if err := a.core.TestSecretProvider(r.Context(), r.PathValue("id")); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
