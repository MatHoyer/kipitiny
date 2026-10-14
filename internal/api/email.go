package api

import (
	"context"
	"net/http"

	"github.com/MatHoyer/kipitiny/internal/core"
)

func (a *API) email(w http.ResponseWriter, r *http.Request) {
	v, err := a.core.Email(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (a *API) setEmailInvites(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Provider string `json:"provider"`
	}
	if !decode(w, r, &body) {
		return
	}
	v, err := a.core.SetEmailInvites(r.Context(), body.Provider)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (a *API) createEmailProvider(w http.ResponseWriter, r *http.Request) {
	var in core.EmailProviderInput
	if !decode(w, r, &in) {
		return
	}
	p, err := a.core.CreateEmailProvider(r.Context(), in)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

func (a *API) updateEmailProvider(w http.ResponseWriter, r *http.Request) {
	var in core.EmailProviderInput
	if !decode(w, r, &in) {
		return
	}
	p, err := a.core.UpdateEmailProvider(r.Context(), r.PathValue("id"), in)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (a *API) deleteEmailProvider(w http.ResponseWriter, r *http.Request) {
	if err := a.core.DeleteEmailProvider(r.Context(), r.PathValue("id")); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) testEmailProvider(w http.ResponseWriter, r *http.Request) {
	var body struct {
		To string `json:"to"`
	}
	if !decode(w, r, &body) {
		return
	}
	id := r.PathValue("id")
	a.test(w, r, func(ctx context.Context) error { return a.core.TestEmailProvider(ctx, id, body.To) })
}
