package api

import (
	"net/http"
	"strconv"

	"github.com/MatHoyer/kipitiny/internal/store"
)

func (a *API) listTokens(w http.ResponseWriter, r *http.Request) {
	ts, err := a.core.ListAPITokens(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ts)
}

func (a *API) createToken(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name  string      `json:"name"`
		Scope store.Scope `json:"scope"`
	}
	if !decode(w, r, &body) {
		return
	}
	t, err := a.core.CreateAPIToken(r.Context(), body.Name, body.Scope)
	if err != nil {
		a.fail(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, t)
}

func (a *API) deleteToken(w http.ResponseWriter, r *http.Request) {
	if err := a.core.DeleteAPIToken(r.Context(), r.PathValue("id")); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) listAudit(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	es, err := a.core.ListAudit(r.Context(), limit)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, es)
}
