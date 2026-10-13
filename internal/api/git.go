package api

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

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

// webhookReadTimeout bounds how long a webhook delivery may take to send
// its body: the server has no read timeout (uploads, streams).
const webhookReadTimeout = 30 * time.Second

// webhookBody reads a webhook's body, at most max bytes, when core asks for
// it: after the sender is known, so unknown callers aren't buffered.
func webhookBody(w http.ResponseWriter, r *http.Request, max int64) func() ([]byte, error) {
	return func() ([]byte, error) {
		_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(webhookReadTimeout))
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, max))
		if err != nil {
			return nil, fmt.Errorf("%w: payload too large or too slow", core.ErrInvalid)
		}
		return body, nil
	}
}

// gitHook receives push webhooks for a git project (GitHub, Gitea, GitLab,
// generic bearer token).
func (a *API) gitHook(w http.ResponseWriter, r *http.Request) {
	err := a.core.HandleGitWebhook(r.Context(), r.PathValue("id"), r.Header.Get, webhookBody(w, r, 5<<20))
	switch {
	case errors.Is(err, core.ErrIgnored):
		writeJSON(w, http.StatusOK, map[string]string{"status": "ignored"})
	case err != nil:
		a.fail(w, err)
	default:
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "syncing"})
	}
}
