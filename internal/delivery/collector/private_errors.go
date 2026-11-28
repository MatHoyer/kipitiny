package collector

import (
	"github.com/kipit-labs/kipitiny/internal/domain/analytics"
	"github.com/kipit-labs/kipitiny/internal/usecase/ingest"

	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// errorRoutes are the manager's, on the private port.
func errorRoutes(mux *http.ServeMux, svc *ingest.Service) {
	num := func(r *http.Request, k string) (int64, error) {
		v := r.PathValue(k)
		if v == "" {
			v = r.URL.Query().Get(k)
		}
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("%w: %s: want a number", analytics.ErrInvalid, k)
		}
		return n, nil
	}
	mux.HandleFunc("PUT /api/error-projects", func(w http.ResponseWriter, r *http.Request) {
		var ps []analytics.ErrorProject
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&ps); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		reply(w, nil, svc.SetErrorProjects(r.Context(), ps))
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
		is, counts, err := svc.Issues(r.Context(), analytics.IssueQuery{
			Project: project, Status: v.Get("status"), Search: v.Get("q"), Sort: v.Get("sort"), Limit: limit, Offset: max(0, offset),
		})
		reply(w, analytics.IssueList{Issues: is, Counts: counts}, err)
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
		is, err := svc.Issue(r.Context(), project, id)
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
		is, err := svc.SetIssueStatus(r.Context(), project, id, in.Status)
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
		ev, err := svc.Event(r.Context(), project, id, r.URL.Query().Get("event"))
		reply(w, ev, err)
	})
	mux.HandleFunc("GET /api/errors/changes", func(w http.ResponseWriter, r *http.Request) {
		after, err := num(r, "after")
		if err != nil {
			reply(w, nil, err)
			return
		}
		cs, last, err := svc.ErrorChanges(r.Context(), after)
		reply(w, analytics.ErrorChanges{Changes: cs, Last: last}, err)
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
		re, err := svc.ReleaseErrors(r.Context(), n[0], r.URL.Query().Get("release"), time.Unix(n[1], 0), time.Unix(n[2], 0))
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
		ps, err := svc.ErrorSeries(r.Context(), n[0], time.Unix(n[1], 0), time.Unix(n[2], 0), time.Duration(n[3])*time.Second)
		reply(w, ps, err)
	})
}
