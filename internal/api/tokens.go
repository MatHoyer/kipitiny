package api

import (
	"net/http"
	"strconv"

	"github.com/MatHoyer/kipitiny/internal/core"
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
	var body core.TokenInput
	if !decode(w, r, &body) {
		return
	}
	t, err := a.core.CreateAPIToken(r.Context(), body)
	if err != nil {
		a.fail(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, t)
}

func (a *API) updateToken(w http.ResponseWriter, r *http.Request) {
	var body core.TokenInput
	if !decode(w, r, &body) {
		return
	}
	t, err := a.core.UpdateAPIToken(r.Context(), r.PathValue("id"), body)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (a *API) rollToken(w http.ResponseWriter, r *http.Request) {
	t, err := a.core.RollAPIToken(r.Context(), r.PathValue("id"))
	if err != nil {
		a.fail(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, t)
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
