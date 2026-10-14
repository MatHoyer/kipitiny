package api

import (
	"net/http"

	"github.com/MatHoyer/kipitiny/internal/core"
)

func (a *API) listSSOProviders(w http.ResponseWriter, r *http.Request) {
	ps, err := a.core.ListSSOProviders(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ps)
}

func (a *API) createSSOProvider(w http.ResponseWriter, r *http.Request) {
	var in core.SSOProviderInput
	if !decode(w, r, &in) {
		return
	}
	p, err := a.core.CreateSSOProvider(r.Context(), in)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

func (a *API) updateSSOProvider(w http.ResponseWriter, r *http.Request) {
	var in core.SSOProviderInput
	if !decode(w, r, &in) {
		return
	}
	p, err := a.core.UpdateSSOProvider(r.Context(), r.PathValue("id"), in)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (a *API) deleteSSOProvider(w http.ResponseWriter, r *http.Request) {
	if err := a.core.DeleteSSOProvider(r.Context(), r.PathValue("id")); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
