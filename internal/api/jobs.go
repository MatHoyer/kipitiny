package api

import (
	"io"
	"net/http"

	"github.com/MatHoyer/kipitiny/internal/core"
)

func (a *API) listJobs(w http.ResponseWriter, r *http.Request) {
	js, err := a.core.ListJobs(r.Context(), r.PathValue("id"))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, js)
}

func (a *API) createJob(w http.ResponseWriter, r *http.Request) {
	var in core.JobInput
	if !decode(w, r, &in) {
		return
	}
	j, err := a.core.CreateJob(r.Context(), r.PathValue("id"), in)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, j)
}

func (a *API) updateJob(w http.ResponseWriter, r *http.Request) {
	var in core.JobInput
	if !decode(w, r, &in) {
		return
	}
	j, err := a.core.UpdateJob(r.Context(), r.PathValue("id"), in)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, j)
}

func (a *API) deleteJob(w http.ResponseWriter, r *http.Request) {
	if err := a.core.DeleteJob(r.Context(), r.PathValue("id")); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) runJob(w http.ResponseWriter, r *http.Request) {
	run, err := a.core.RunJob(r.Context(), r.PathValue("id"))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, run)
}

func (a *API) listJobRuns(w http.ResponseWriter, r *http.Request) {
	runs, err := a.core.ListJobRuns(r.Context(), r.PathValue("id"))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, runs)
}

func (a *API) jobRunLog(w http.ResponseWriter, r *http.Request) {
	rc, err := a.core.JobRunLog(r.Context(), r.PathValue("id"), r.PathValue("run"))
	if err != nil {
		a.fail(w, err)
		return
	}
	defer rc.Close()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.Copy(w, rc)
}
