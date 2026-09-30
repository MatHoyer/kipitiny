package api

import "net/http"

func (a *API) listDomains(w http.ResponseWriter, r *http.Request) {
	ds, err := a.core.ListDomains(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ds)
}

func (a *API) createDomain(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &body) {
		return
	}
	d, err := a.core.CreateDomain(r.Context(), body.Name)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, d)
}

func (a *API) deleteDomain(w http.ResponseWriter, r *http.Request) {
	if err := a.core.DeleteDomain(r.Context(), r.PathValue("id")); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
