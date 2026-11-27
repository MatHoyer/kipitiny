package api

import (
	"net/http"

	"github.com/kipit-labs/kipitiny/internal/usecase/servers"
)

func (a *API) getCleanup(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.core.Cleanup(r.Context()))
}

func (a *API) setCleanup(w http.ResponseWriter, r *http.Request) {
	var body servers.CleanupSettings
	if !decode(w, r, &body) {
		return
	}
	v, err := a.core.SetCleanup(r.Context(), body)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (a *API) runCleanup(w http.ResponseWriter, r *http.Request) {
	v, err := a.core.RunCleanup(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, v)
}
