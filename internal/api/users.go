package api

import (
	"net/http"

	"github.com/MatHoyer/kipitiny/internal/core"
)

func (a *API) listUsers(w http.ResponseWriter, r *http.Request) {
	us, err := a.core.ListUsers(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, us)
}

func (a *API) setUserAccess(w http.ResponseWriter, r *http.Request) {
	var body core.UserAccess
	if !decode(w, r, &body) {
		return
	}
	u, err := a.core.SetUserAccess(r.Context(), r.PathValue("id"), body)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

func (a *API) deleteUser(w http.ResponseWriter, r *http.Request) {
	if err := a.core.DeleteUser(r.Context(), r.PathValue("id")); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) listInvites(w http.ResponseWriter, r *http.Request) {
	is, err := a.core.ListInvites(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, is)
}

func (a *API) createInvite(w http.ResponseWriter, r *http.Request) {
	var body core.UserAccess
	if !decode(w, r, &body) {
		return
	}
	i, err := a.core.CreateInvite(r.Context(), body)
	if err != nil {
		a.fail(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, i)
}

func (a *API) deleteInvite(w http.ResponseWriter, r *http.Request) {
	if err := a.core.DeleteInvite(r.Context(), r.PathValue("id")); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// invite describes an invite link before it's accepted. Guessing tokens
// counts like failed logins.
func (a *API) invite(w http.ResponseWriter, r *http.Request) {
	ip := a.clientIP(r)
	if !a.limiter.allow(ip) {
		writeError(w, http.StatusTooManyRequests, "too many attempts, try again later")
		return
	}
	var body struct {
		Token string `json:"token"`
	}
	if !decode(w, r, &body) {
		return
	}
	info, err := a.core.Invite(r.Context(), body.Token)
	if err != nil {
		a.limiter.fail(ip)
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// acceptInvite creates the invited account and signs it in.
func (a *API) acceptInvite(w http.ResponseWriter, r *http.Request) {
	ip := a.clientIP(r)
	if !a.limiter.allow(ip) {
		writeError(w, http.StatusTooManyRequests, "too many attempts, try again later")
		return
	}
	var body struct {
		Token    string `json:"token"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decode(w, r, &body) {
		return
	}
	u, token, err := a.core.AcceptInvite(r.Context(), body.Token, body.Username, body.Password)
	if err != nil {
		a.limiter.fail(ip)
		a.fail(w, err)
		return
	}
	setSessionCookie(w, r, token, int(core.SessionTTL.Seconds()))
	writeJSON(w, http.StatusCreated, u)
}
