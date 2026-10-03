package api

import (
	"errors"
	"net/http"

	"github.com/MatHoyer/kipitiny/internal/core"
)

// The signed-in user's own security settings. Changes that alter how the
// account signs in ask for the password again, rate-limited like a login.

type passwordBody struct {
	Password string `json:"password"`
}

// confirmAllowed refuses while the caller's IP is locked out.
func (a *API) confirmAllowed(w http.ResponseWriter, r *http.Request) bool {
	if !a.limiter.allow(clientIP(r, a.core.BehindTunnel(r.Context()))) {
		writeError(w, http.StatusTooManyRequests, "too many attempts, try again later")
		return false
	}
	return true
}

// failConfirm counts a wrong password against the caller's IP.
func (a *API) failConfirm(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, core.ErrWrongPassword) {
		a.limiter.fail(clientIP(r, a.core.BehindTunnel(r.Context())))
	}
	a.fail(w, err)
}

func (a *API) account(w http.ResponseWriter, r *http.Request) {
	acc, err := a.core.Account(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, acc)
}

func (a *API) changePassword(w http.ResponseWriter, r *http.Request) {
	if !a.confirmAllowed(w, r) {
		return
	}
	var body struct {
		Current  string `json:"current"`
		Password string `json:"password"`
	}
	if !decode(w, r, &body) {
		return
	}
	token, err := a.core.ChangePassword(r.Context(), body.Current, body.Password)
	if err != nil {
		a.failConfirm(w, r, err)
		return
	}
	setSessionCookie(w, r, token, int(core.SessionTTL.Seconds()))
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) beginTOTP(w http.ResponseWriter, r *http.Request) {
	if !a.confirmAllowed(w, r) {
		return
	}
	var body passwordBody
	if !decode(w, r, &body) {
		return
	}
	setup, err := a.core.BeginTOTP(r.Context(), body.Password)
	if err != nil {
		a.failConfirm(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, setup)
}

type recoveryCodes struct {
	RecoveryCodes []string `json:"recoveryCodes"`
}

func (a *API) enableTOTP(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code string `json:"code"`
	}
	if !decode(w, r, &body) {
		return
	}
	codes, err := a.core.EnableTOTP(r.Context(), body.Code)
	if err != nil {
		a.fail(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, recoveryCodes{codes})
}

func (a *API) disableTOTP(w http.ResponseWriter, r *http.Request) {
	if !a.confirmAllowed(w, r) {
		return
	}
	var body passwordBody
	if !decode(w, r, &body) {
		return
	}
	if err := a.core.DisableTOTP(r.Context(), body.Password); err != nil {
		a.failConfirm(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) regenerateRecoveryCodes(w http.ResponseWriter, r *http.Request) {
	if !a.confirmAllowed(w, r) {
		return
	}
	var body passwordBody
	if !decode(w, r, &body) {
		return
	}
	codes, err := a.core.RegenerateRecoveryCodes(r.Context(), body.Password)
	if err != nil {
		a.failConfirm(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, recoveryCodes{codes})
}
