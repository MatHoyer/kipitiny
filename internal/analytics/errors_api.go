package analytics

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// IssueList is a page of issues, and how many each status has.
type IssueList struct {
	Issues []Issue        `json:"issues"`
	Counts map[string]int `json:"counts"`
}

// errorRoutes are the manager's, on the private port.
func errorRoutes(mux *http.ServeMux, st *Store) {
	num := func(r *http.Request, k string) (int64, error) {
		v := r.PathValue(k)
		if v == "" {
			v = r.URL.Query().Get(k)
		}
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("%w: %s: want a number", ErrInvalid, k)
		}
		return n, nil
	}
	mux.HandleFunc("PUT /api/error-projects", func(w http.ResponseWriter, r *http.Request) {
		var ps []ErrorProject
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&ps); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		reply(w, nil, st.SetErrorProjects(r.Context(), ps))
	})
	mux.HandleFunc("GET /api/issues", func(w http.ResponseWriter, r *http.Request) {
		v := r.URL.Query()
		project, err := num(r, "project")
		if err != nil {
			reply(w, nil, err)
			return
		}
		limit, _ := strconv.Atoi(v.Get("limit"))
		offset, _ := strconv.Atoi(v.Get("offset"))
		is, counts, err := st.Issues(r.Context(), IssueQuery{
			Project: project, Status: v.Get("status"), Search: v.Get("q"), Sort: v.Get("sort"), Limit: limit, Offset: max(0, offset),
		})
		reply(w, IssueList{Issues: is, Counts: counts}, err)
	})
	mux.HandleFunc("GET /api/issues/{id}", func(w http.ResponseWriter, r *http.Request) {
		project, err := num(r, "project")
		if err != nil {
			reply(w, nil, err)
			return
		}
		id, err := num(r, "id")
		if err != nil {
			reply(w, nil, err)
			return
		}
		is, err := st.Issue(r.Context(), project, id)
		reply(w, is, err)
	})
	mux.HandleFunc("PUT /api/issues/{id}/status", func(w http.ResponseWriter, r *http.Request) {
		project, err := num(r, "project")
		if err != nil {
			reply(w, nil, err)
			return
		}
		id, err := num(r, "id")
		if err != nil {
			reply(w, nil, err)
			return
		}
		var in struct {
			Status string `json:"status"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<10)).Decode(&in); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		is, err := st.SetIssueStatus(r.Context(), project, id, in.Status)
		reply(w, is, err)
	})
	mux.HandleFunc("GET /api/issues/{id}/event", func(w http.ResponseWriter, r *http.Request) {
		project, err := num(r, "project")
		if err != nil {
			reply(w, nil, err)
			return
		}
		id, err := num(r, "id")
		if err != nil {
			reply(w, nil, err)
			return
		}
		ev, err := st.Event(r.Context(), project, id, r.URL.Query().Get("event"))
		reply(w, ev, err)
	})
	mux.HandleFunc("GET /api/errors/changes", func(w http.ResponseWriter, r *http.Request) {
		after, err := num(r, "after")
		if err != nil {
			reply(w, nil, err)
			return
		}
		cs, last, err := st.ErrorChanges(r.Context(), after)
		reply(w, ErrorChanges{Changes: cs, Last: last}, err)
	})
	mux.HandleFunc("GET /api/errors/release", func(w http.ResponseWriter, r *http.Request) {
		var n [3]int64
		for i, k := range []string{"project", "from", "to"} {
			v, err := num(r, k)
			if err != nil {
				reply(w, nil, err)
				return
			}
			n[i] = v
		}
		re, err := st.ReleaseErrors(r.Context(), n[0], r.URL.Query().Get("release"), time.Unix(n[1], 0), time.Unix(n[2], 0))
		reply(w, re, err)
	})
	mux.HandleFunc("GET /api/errors/series", func(w http.ResponseWriter, r *http.Request) {
		var n [4]int64
		for i, k := range []string{"project", "from", "to", "interval"} {
			v, err := num(r, k)
			if err != nil {
				reply(w, nil, err)
				return
			}
			n[i] = v
		}
		ps, err := st.ErrorSeries(r.Context(), n[0], time.Unix(n[1], 0), time.Unix(n[2], 0), time.Duration(n[3])*time.Second)
		reply(w, ps, err)
	})
}

// SetErrorProjects sends the apps taking errors.
func (c *Client) SetErrorProjects(ctx context.Context, ps []ErrorProject) error {
	if ps == nil {
		ps = []ErrorProject{}
	}
	return c.do(ctx, http.MethodPut, "/api/error-projects", ps, nil)
}

func (c *Client) Issues(ctx context.Context, q IssueQuery) (IssueList, error) {
	v := url.Values{"project": {strconv.FormatInt(q.Project, 10)}, "status": {q.Status}, "q": {q.Search}, "sort": {q.Sort},
		"limit": {strconv.Itoa(q.Limit)}, "offset": {strconv.Itoa(q.Offset)}}
	var l IssueList
	err := c.do(ctx, http.MethodGet, "/api/issues?"+v.Encode(), nil, &l)
	return l, err
}

func issuePath(project, id int64, suffix string) string {
	return "/api/issues/" + strconv.FormatInt(id, 10) + suffix + "?project=" + strconv.FormatInt(project, 10)
}

func (c *Client) Issue(ctx context.Context, project, id int64) (Issue, error) {
	var is Issue
	err := c.do(ctx, http.MethodGet, issuePath(project, id, ""), nil, &is)
	return is, err
}

func (c *Client) SetIssueStatus(ctx context.Context, project, id int64, status string) (Issue, error) {
	var is Issue
	err := c.do(ctx, http.MethodPut, issuePath(project, id, "/status"), map[string]string{"status": status}, &is)
	return is, err
}

// Event is one of the issue's kept events, its latest when event is empty.
func (c *Client) Event(ctx context.Context, project, id int64, event string) (IssueEvent, error) {
	var ev IssueEvent
	err := c.do(ctx, http.MethodGet, issuePath(project, id, "/event")+"&event="+url.QueryEscape(event), nil, &ev)
	return ev, err
}

func (c *Client) ErrorSeries(ctx context.Context, project int64, from, to time.Time, interval time.Duration) ([]ErrorPoint, error) {
	v := url.Values{}
	v.Set("project", strconv.FormatInt(project, 10))
	v.Set("from", strconv.FormatInt(from.Unix(), 10))
	v.Set("to", strconv.FormatInt(to.Unix(), 10))
	v.Set("interval", strconv.FormatInt(int64(interval.Seconds()), 10))
	var ps []ErrorPoint
	err := c.do(ctx, http.MethodGet, "/api/errors/series?"+v.Encode(), nil, &ps)
	return ps, err
}

// ErrorChanges is a page of changes, and the last seq there is.
type ErrorChanges struct {
	Changes []ErrorChange `json:"changes"`
	Last    int64         `json:"last"`
}

// ErrorChanges are the issues opened or reopened after seq.
func (c *Client) ErrorChanges(ctx context.Context, after int64) (ErrorChanges, error) {
	var out ErrorChanges
	err := c.do(ctx, http.MethodGet, "/api/errors/changes?after="+strconv.FormatInt(after, 10), nil, &out)
	return out, err
}

// ReleaseErrors counts project's events of release in [from, to).
func (c *Client) ReleaseErrors(ctx context.Context, project int64, release string, from, to time.Time) (ReleaseErrors, error) {
	v := url.Values{"release": {release}}
	v.Set("project", strconv.FormatInt(project, 10))
	v.Set("from", strconv.FormatInt(from.Unix(), 10))
	v.Set("to", strconv.FormatInt(to.Unix(), 10))
	var out ReleaseErrors
	err := c.do(ctx, http.MethodGet, "/api/errors/release?"+v.Encode(), nil, &out)
	return out, err
}
