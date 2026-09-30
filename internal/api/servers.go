package api

import (
	"net/http"

	"github.com/MatHoyer/kipitiny/internal/core"
)

func (a *API) listServers(w http.ResponseWriter, r *http.Request) {
	ss, err := a.core.ListServers(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ss)
}

func (a *API) createServer(w http.ResponseWriter, r *http.Request) {
	var in core.ServerInput
	if !decode(w, r, &in) {
		return
	}
	sv, err := a.core.CreateServer(r.Context(), in)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, sv)
}

func (a *API) updateServer(w http.ResponseWriter, r *http.Request) {
	var in core.ServerInput
	if !decode(w, r, &in) {
		return
	}
	sv, err := a.core.UpdateServer(r.Context(), r.PathValue("id"), in)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sv)
}

func (a *API) deleteServer(w http.ResponseWriter, r *http.Request) {
	if err := a.core.DeleteServer(r.Context(), r.PathValue("id")); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) sshKey(w http.ResponseWriter, r *http.Request) {
	key, err := a.core.SSHPublicKey(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"publicKey": key})
}
