package api

import (
	"net/http"
	"strconv"

	"github.com/kipit-labs/kipitiny/internal/core"
)

// serviceErrors takes status, q, sort, limit and offset.
func (a *API) serviceErrors(w http.ResponseWriter, r *http.Request) {
	v := r.URL.Query()
	limit, _ := strconv.Atoi(v.Get("limit"))
	offset, _ := strconv.Atoi(v.Get("offset"))
	res, err := a.core.ServiceErrors(r.Context(), r.PathValue("id"), core.ErrorsQuery{
		Status: v.Get("status"), Search: v.Get("q"), Sort: v.Get("sort"), Limit: limit, Offset: offset,
	})
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// serviceErrorSeries takes from, to and interval as analyticsQuery does.
func (a *API) serviceErrorSeries(w http.ResponseWriter, r *http.Request) {
	q := analyticsQuery(r)
	res, err := a.core.ServiceErrorSeries(r.Context(), r.PathValue("id"), q.From, q.To, q.Interval)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (a *API) serviceDeployErrors(w http.ResponseWriter, r *http.Request) {
	res, err := a.core.ServiceDeployErrors(r.Context(), r.PathValue("id"))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func issueID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("issue"), 10, 64)
	if err != nil {
		http.Error(w, "issue: want a number", http.StatusBadRequest)
		return 0, false
	}
	return id, true
}

func (a *API) serviceIssue(w http.ResponseWriter, r *http.Request) {
	id, ok := issueID(w, r)
	if !ok {
		return
	}
	res, err := a.core.ServiceIssue(r.Context(), r.PathValue("id"), id)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// serviceIssueEvent takes event, an event id (default the latest).
func (a *API) serviceIssueEvent(w http.ResponseWriter, r *http.Request) {
	id, ok := issueID(w, r)
	if !ok {
		return
	}
	res, err := a.core.ServiceIssueEvent(r.Context(), r.PathValue("id"), id, r.URL.Query().Get("event"))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (a *API) setServiceIssueStatus(w http.ResponseWriter, r *http.Request) {
	id, ok := issueID(w, r)
	if !ok {
		return
	}
	var body struct {
		Status string `json:"status"`
	}
	if !decode(w, r, &body) {
		return
	}
	res, err := a.core.SetServiceIssueStatus(r.Context(), r.PathValue("id"), id, body.Status)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}
