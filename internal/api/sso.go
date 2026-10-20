package api

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/MatHoyer/kipitiny/internal/core"
)

func (a *API) listSSOProviders(w http.ResponseWriter, r *http.Request) {
	ps, err := a.core.ListSSOProviders(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ps)
}

func (a *API) createSSOProvider(w http.ResponseWriter, r *http.Request) {
	var in core.SSOProviderInput
	if !decode(w, r, &in) {
		return
	}
	p, err := a.core.CreateSSOProvider(r.Context(), in)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

func (a *API) updateSSOProvider(w http.ResponseWriter, r *http.Request) {
	var in core.SSOProviderInput
	if !decode(w, r, &in) {
		return
	}
	p, err := a.core.UpdateSSOProvider(r.Context(), r.PathValue("id"), in)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (a *API) deleteSSOProvider(w http.ResponseWriter, r *http.Request) {
	if err := a.core.DeleteSSOProvider(r.Context(), r.PathValue("id")); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// invitePage is the UI's page for invite links.
const invitePage = "/invite"

// ssoCookie binds a sign-in at a provider to the browser that started it.
const ssoCookie = "kipitiny_sso"

func setSSOCookie(w http.ResponseWriter, r *http.Request, state string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     ssoCookie,
		Value:    state,
		Path:     core.SSOCallbackPath,
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   isHTTPS(r),
		// Lax: the provider sends the browser back with a top-level GET.
		SameSite: http.SameSiteLaxMode,
	})
}

// startSSO answers with the provider's page, the flow's state in a cookie.
func (a *API) startSSO(w http.ResponseWriter, r *http.Request, start core.SSOStart) {
	setSSOCookie(w, r, start.State, int((10 * time.Minute).Seconds()))
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, start)
}

func (a *API) beginSSOLogin(w http.ResponseWriter, r *http.Request) {
	ip := a.clientIP(r)
	if !a.begins.allow(r.Context(), ip) {
		writeError(w, http.StatusTooManyRequests, "too many attempts, try again later")
		return
	}
	a.begins.fail(r.Context(), ip) // every start holds server memory
	var body struct {
		ReturnTo string `json:"returnTo"`
	}
	if !decode(w, r, &body) {
		return
	}
	start, err := a.core.BeginSSO(r.Context(), r.PathValue("id"), relyingParty(r).Origin, body.ReturnTo)
	if err != nil {
		a.fail(w, err)
		return
	}
	a.startSSO(w, r, start)
}

func (a *API) beginSSOInvite(w http.ResponseWriter, r *http.Request) {
	ip := a.clientIP(r)
	if !a.begins.allow(r.Context(), ip) {
		writeError(w, http.StatusTooManyRequests, "too many attempts, try again later")
		return
	}
	a.begins.fail(r.Context(), ip)
	var body struct {
		Token    string `json:"token"`
		Username string `json:"username"`
	}
	if !decode(w, r, &body) {
		return
	}
	start, err := a.core.BeginSSOInvite(r.Context(), r.PathValue("id"), relyingParty(r).Origin, body.Token, body.Username)
	if err != nil {
		a.fail(w, err)
		return
	}
	a.startSSO(w, r, start)
}

func (a *API) beginSSOConnect(w http.ResponseWriter, r *http.Request) {
	if !a.confirmAllowed(w, r) {
		return
	}
	var body passwordBody
	if !decode(w, r, &body) {
		return
	}
	start, err := a.core.BeginSSOConnect(r.Context(), r.PathValue("id"), relyingParty(r).Origin, body.Password)
	if err != nil {
		a.failConfirm(w, r, err)
		return
	}
	a.startSSO(w, r, start)
}

func (a *API) unlinkSSO(w http.ResponseWriter, r *http.Request) {
	if err := a.core.UnlinkSSOIdentity(r.Context(), r.PathValue("id")); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ssoCallback is where providers send the browser back. It answers with a
// redirect: to the page the sign-in started from, with the outcome.
func (a *API) ssoCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	cookie := ""
	if c, err := r.Cookie(ssoCookie); err == nil {
		cookie = c.Value
	}
	setSSOCookie(w, r, "", -1)
	w.Header().Set("Cache-Control", "no-store")
	var res core.SSOResult
	var err error
	if e := q.Get("error"); e != "" {
		msg := q.Get("error_description")
		if msg == "" {
			msg = e
		}
		// The flow is dropped all the same: its state is single use.
		res, _ = a.core.FinishSSO(r.Context(), q.Get("state"), "", "")
		err = fmt.Errorf("%w: the provider refused the sign-in: %s", core.ErrInvalid, msg)
	} else {
		res, err = a.core.FinishSSO(r.Context(), q.Get("state"), cookie, q.Get("code"))
	}

	if res.Purpose == "connect" {
		ctx := r.Context()
		if res.User.ID != "" {
			if actor, aerr := a.core.UserActor(ctx, res.User); aerr == nil {
				ctx = core.WithActor(ctx, actor)
			}
		}
		status := http.StatusSeeOther
		if err != nil {
			status = http.StatusBadRequest
		}
		a.core.Audit(ctx, "POST /api/account/sso/{id}/connect", "", status, err)
		a.ssoRedirect(w, r, "/account", "sso-connected", err)
		return
	}
	if res.Purpose == "confirm" {
		to := res.ReturnTo
		if to == "" {
			to = "/account"
		}
		a.ssoRedirect(w, r, to, "sso-confirmed", err)
		return
	}
	if res.Purpose == "invite" && err != nil {
		// Back to the invite, its token in the fragment as in the link.
		a.ssoRedirect(w, r, invitePage+"#"+url.QueryEscape(res.Invite), "", err)
		return
	}
	if err == nil && res.MFATicket != "" {
		// The ticket stays out of logs and Referer in the fragment.
		http.Redirect(w, r, res.ReturnTo+"#sso-mfa="+url.QueryEscape(res.MFATicket), http.StatusSeeOther)
		return
	}
	if err == nil {
		setSessionCookie(w, r, res.Session, int(core.SessionTTL.Seconds()))
	}
	to := res.ReturnTo
	if to == "" {
		to = "/"
	}
	a.ssoRedirect(w, r, to, "", err)
}

// ssoRedirect sends the browser to page, with done as a flag on success or
// the error to show.
func (a *API) ssoRedirect(w http.ResponseWriter, r *http.Request, page, done string, err error) {
	u, _ := url.Parse(page)
	q := u.Query()
	switch {
	case err != nil:
		msg := "the sign-in failed; see the manager logs"
		switch {
		case errors.Is(err, core.ErrInvalid):
			msg = reason(err, core.ErrInvalid)
		case errors.Is(err, core.ErrUnauthorized):
			msg = reason(err, core.ErrUnauthorized)
		default:
			a.log.Error("sso sign-in failed", "err", err)
		}
		q.Set("sso-error", msg)
	case done != "":
		q.Set(done, "1")
	}
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusSeeOther)
}

func (a *API) ssoRequired(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"required": a.core.SSORequired(r.Context())})
}

func (a *API) setSSORequired(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Required bool `json:"required"`
	}
	if !decode(w, r, &body) {
		return
	}
	if err := a.core.SetSSORequired(r.Context(), body.Required); err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, body)
}

func (a *API) beginSSOConfirm(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ReturnTo string `json:"returnTo"`
	}
	if !decode(w, r, &body) {
		return
	}
	start, err := a.core.BeginSSOConfirm(r.Context(), r.PathValue("id"), relyingParty(r).Origin, body.ReturnTo)
	if err != nil {
		a.fail(w, err)
		return
	}
	a.startSSO(w, r, start)
}
