package api

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/MatHoyer/kipitiny/internal/core"
)

// gitProvidersPage is where the browser lands after connecting a provider
// on the forge.
const gitProvidersPage = "/settings/git-providers"

func (a *API) listGitProviders(w http.ResponseWriter, r *http.Request) {
	ps, err := a.core.ListGitProviders(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ps)
}

func (a *API) createGitProvider(w http.ResponseWriter, r *http.Request) {
	var in core.GitProviderInput
	if !decode(w, r, &in) {
		return
	}
	p, err := a.core.CreateGitProvider(r.Context(), in)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

func (a *API) updateGitProvider(w http.ResponseWriter, r *http.Request) {
	var in core.GitProviderInput
	if !decode(w, r, &in) {
		return
	}
	p, err := a.core.UpdateGitProvider(r.Context(), r.PathValue("id"), in)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (a *API) deleteGitProvider(w http.ResponseWriter, r *http.Request) {
	if err := a.core.DeleteGitProvider(r.Context(), r.PathValue("id")); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) testGitProvider(w http.ResponseWriter, r *http.Request) {
	if err := a.core.TestGitProvider(r.Context(), r.PathValue("id")); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) gitProviderRepos(w http.ResponseWriter, r *http.Request) {
	repos, err := a.core.GitProviderRepos(r.Context(), r.PathValue("id"))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, repos)
}

func (a *API) gitProviderBranches(w http.ResponseWriter, r *http.Request) {
	bs, err := a.core.GitProviderBranches(r.Context(), r.PathValue("id"), r.URL.Query().Get("repo"))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, bs)
}

// authorizeGitProvider returns the forge page that connects the provider.
func (a *API) authorizeGitProvider(w http.ResponseWriter, r *http.Request) {
	u, err := a.core.AuthorizeGitProvider(r.Context(), r.PathValue("id"), relyingParty(r).Origin)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": u})
}

// startGitHubApp returns the manifest form the browser posts to GitHub.
func (a *API) startGitHubApp(w http.ResponseWriter, r *http.Request) {
	var in core.GitHubAppInput
	if !decode(w, r, &in) {
		return
	}
	start, err := a.core.StartGitHubApp(r.Context(), relyingParty(r).Origin, in)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, start)
}

// The forge sends the browser back to these; they answer with redirects
// (to the next step, or the settings page with the outcome) and are
// audited although they are GETs.

func (a *API) gitHubAppCreated(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	next, err := a.core.FinishGitHubApp(r.Context(), q.Get("code"), q.Get("state"))
	a.gitProviderRedirect(w, r, "", next, err)
}

func (a *API) gitHubAppInstalled(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	installation, err := strconv.ParseInt(q.Get("installation_id"), 10, 64)
	if err != nil {
		a.gitProviderRedirect(w, r, "", "", errors.New("GitHub didn't say which installation"))
		return
	}
	id, err := a.core.GitHubAppInstalled(r.Context(), installation, q.Get("state"))
	a.gitProviderRedirect(w, r, id, "", err)
}

func (a *API) gitProviderOAuthCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		msg := q.Get("error_description")
		if msg == "" {
			msg = e
		}
		a.gitProviderRedirect(w, r, "", "", errors.New("the authorization was refused: "+msg))
		return
	}
	id, err := a.core.FinishGitProviderOAuth(r.Context(), q.Get("code"), q.Get("state"))
	a.gitProviderRedirect(w, r, id, "", err)
}

// gitProviderRedirect sends the browser to next, or back to the settings
// page with the connected provider or the error.
func (a *API) gitProviderRedirect(w http.ResponseWriter, r *http.Request, id, next string, err error) {
	pattern := "GET " + r.URL.Path
	status := http.StatusSeeOther
	if err != nil {
		status = http.StatusBadRequest
	}
	a.core.Audit(r.Context(), pattern, id, status, err)
	if err != nil {
		if !errors.Is(err, core.ErrInvalid) {
			a.log.Error("git provider connection failed", "err", err)
		}
		next = gitProvidersPage + "?error=" + url.QueryEscape(reason(err, core.ErrInvalid))
	} else if next == "" {
		next = gitProvidersPage + "?connected=" + url.QueryEscape(id)
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

// gitProviderHook receives a GitHub App's push webhooks.
func (a *API) gitProviderHook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 25<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "payload too large")
		return
	}
	err = a.core.HandleGitProviderWebhook(r.Context(), r.PathValue("id"), r.Header.Get, body)
	switch {
	case errors.Is(err, core.ErrIgnored):
		writeJSON(w, http.StatusOK, map[string]string{"status": "ignored"})
	case err != nil:
		a.fail(w, err)
	default:
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "syncing"})
	}
}
