package api

import (
	"context"
	"net/http"

	"github.com/MatHoyer/kipitiny/internal/mail"
)

func (a *API) email(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.core.Email(r.Context()))
}

func (a *API) setEmail(w http.ResponseWriter, r *http.Request) {
	var body mail.Config
	if !decode(w, r, &body) {
		return
	}
	v, err := a.core.SetEmail(r.Context(), body)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (a *API) deleteEmail(w http.ResponseWriter, r *http.Request) {
	if err := a.core.DeleteEmail(r.Context()); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) testEmail(w http.ResponseWriter, r *http.Request) {
	var body struct {
		To string `json:"to"`
	}
	if !decode(w, r, &body) {
		return
	}
	a.test(w, r, func(ctx context.Context) error { return a.core.TestEmail(ctx, body.To) })
}
