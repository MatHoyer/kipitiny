package api

import (
	"net/http"

	"github.com/MatHoyer/kipitiny/internal/core"
)

func (a *API) notifications(w http.ResponseWriter, r *http.Request) {
	v, err := a.core.Notifications(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (a *API) createChannel(w http.ResponseWriter, r *http.Request) {
	var body core.ChannelInput
	if !decode(w, r, &body) {
		return
	}
	ch, err := a.core.CreateChannel(r.Context(), body)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, ch)
}

func (a *API) updateChannel(w http.ResponseWriter, r *http.Request) {
	var body core.ChannelInput
	if !decode(w, r, &body) {
		return
	}
	ch, err := a.core.UpdateChannel(r.Context(), r.PathValue("id"), body)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ch)
}

func (a *API) deleteChannel(w http.ResponseWriter, r *http.Request) {
	if err := a.core.DeleteChannel(r.Context(), r.PathValue("id")); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) testChannel(w http.ResponseWriter, r *http.Request) {
	if err := a.core.TestChannel(r.Context(), r.PathValue("id")); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
