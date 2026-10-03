package api

import "net/http"

func (a *API) topology(w http.ResponseWriter, r *http.Request) {
	t, err := a.core.Topology(r.Context(), r.PathValue("id"))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}
