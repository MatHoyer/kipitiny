package api

import (
	"errors"
	"io"
	"net/http"

	"github.com/MatHoyer/kipitiny/internal/core"
)

func (a *API) projectGit(w http.ResponseWriter, r *http.Request) {
	st, err := a.core.GetProjectGit(r.Context(), r.PathValue("id"))
	if err != nil {
		a.fail(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, st)
}

func (a *API) linkProjectGit(w http.ResponseWriter, r *http.Request) {
	var in core.GitInput
	if !decode(w, r, &in) {
		return
	}
	st, err := a.core.LinkProjectGit(r.Context(), r.PathValue("id"), in)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (a *API) previewProjectGit(w http.ResponseWriter, r *http.Request) {
	var in core.GitInput
	if !decode(w, r, &in) {
		return
	}
	plan, err := a.core.PreviewProjectGit(r.Context(), r.PathValue("id"), in)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

func (a *API) unlinkProjectGit(w http.ResponseWriter, r *http.Request) {
	if err := a.core.UnlinkProjectGit(r.Context(), r.PathValue("id")); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) syncProjectGit(w http.ResponseWriter, r *http.Request) {
	plan, err := a.core.SyncProjectGit(r.Context(), r.PathValue("id"), r.URL.Query().Get("dryRun") != "")
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

// gitHook receives push webhooks for a git project (GitHub, Gitea, GitLab,
// generic bearer token).
func (a *API) gitHook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 5<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "payload too large")
		return
	}
	err = a.core.HandleGitWebhook(r.Context(), r.PathValue("id"), r.Header.Get, body)
	switch {
	case errors.Is(err, core.ErrIgnored):
		writeJSON(w, http.StatusOK, map[string]string{"status": "ignored"})
	case err != nil:
		a.fail(w, err)
	default:
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "syncing"})
	}
}
