package api

import (
	"net/http"

	"github.com/MatHoyer/kipitiny/internal/core"
)

func (a *API) listRegistries(w http.ResponseWriter, r *http.Request) {
	rs, err := a.core.ListRegistries(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rs)
}

func (a *API) createRegistry(w http.ResponseWriter, r *http.Request) {
	var in core.RegistryInput
	if !decode(w, r, &in) {
		return
	}
	reg, err := a.core.CreateRegistry(r.Context(), in)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, reg)
}

func (a *API) updateRegistry(w http.ResponseWriter, r *http.Request) {
	var in core.RegistryInput
	if !decode(w, r, &in) {
		return
	}
	reg, err := a.core.UpdateRegistry(r.Context(), r.PathValue("id"), in)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, reg)
}

func (a *API) deleteRegistry(w http.ResponseWriter, r *http.Request) {
	if err := a.core.DeleteRegistry(r.Context(), r.PathValue("id")); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) testRegistry(w http.ResponseWriter, r *http.Request) {
	if err := a.core.TestRegistry(r.Context(), r.PathValue("id")); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
