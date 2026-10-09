package api

import (
	"net/http"

	"github.com/MatHoyer/kipitiny/internal/core"
)

func (a *API) listNetworks(w http.ResponseWriter, r *http.Request) {
	ns, err := a.core.ListNetworks(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ns)
}

func (a *API) createNetwork(w http.ResponseWriter, r *http.Request) {
	var in core.NetworkInput
	if !decode(w, r, &in) {
		return
	}
	n, err := a.core.CreateNetwork(r.Context(), in)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, n)
}

func (a *API) deleteNetwork(w http.ResponseWriter, r *http.Request) {
	if err := a.core.DeleteNetwork(r.Context(), r.PathValue("id")); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) setServiceNetworks(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Networks []string `json:"networks"`
	}
	if !decode(w, r, &in) {
		return
	}
	v, err := a.core.SetServiceNetworks(r.Context(), r.PathValue("id"), in.Networks)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}
