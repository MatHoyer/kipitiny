package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/MatHoyer/kipitiny/internal/core"
)

func (a *API) pgTables(w http.ResponseWriter, r *http.Request) {
	ts, err := a.core.PgTables(r.Context(), r.PathValue("id"))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ts)
}

func (a *API) pgRows(w http.ResponseWriter, r *http.Request) {
	q, ok := rowQuery(w, r)
	if !ok {
		return
	}
	rows, err := a.core.PgRows(r.Context(), r.PathValue("id"), r.PathValue("schema"), r.PathValue("table"), q)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (a *API) pgExport(w http.ResponseWriter, r *http.Request) {
	q, ok := rowQuery(w, r)
	if !ok {
		return
	}
	schema, table := r.PathValue("schema"), r.PathValue("table")
	out := &lazyHeaders{w: w, set: func(h http.Header) {
		h.Set("Content-Type", "text/csv; charset=utf-8")
		h.Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", schema+"."+table+".csv"))
	}}
	err := a.core.PgExport(r.Context(), r.PathValue("id"), schema, table, q, out)
	switch {
	case err != nil && !out.wrote:
		a.fail(w, err)
	case err != nil:
		a.log.Warn("export interrupted", "service", r.PathValue("id"), "err", err)
	case !out.wrote: // no rows and no header: send an empty file
		out.set(w.Header())
		w.WriteHeader(http.StatusOK)
	}
}

// rowQuery reads paging, sorting and filters (a JSON array) from the URL.
func rowQuery(w http.ResponseWriter, r *http.Request) (core.RowQuery, bool) {
	v := r.URL.Query()
	q := core.RowQuery{OrderBy: v.Get("order"), Desc: v.Get("desc") == "true"}
	q.Limit, _ = strconv.Atoi(v.Get("limit"))
	q.Offset, _ = strconv.Atoi(v.Get("offset"))
	if f := v.Get("filters"); f != "" {
		if err := json.Unmarshal([]byte(f), &q.Filters); err != nil {
			writeError(w, http.StatusBadRequest, "invalid filters: "+err.Error())
			return q, false
		}
	}
	return q, true
}

// lazyHeaders sets the download headers on the first byte, so an error
// before any output can still be answered as JSON.
type lazyHeaders struct {
	w     http.ResponseWriter
	set   func(http.Header)
	wrote bool
}

func (l *lazyHeaders) Write(p []byte) (int, error) {
	if !l.wrote {
		l.wrote = true
		l.set(l.w.Header())
	}
	return l.w.Write(p)
}

func (a *API) redisScan(w http.ResponseWriter, r *http.Request) {
	v := r.URL.Query()
	count, _ := strconv.Atoi(v.Get("count"))
	keys, err := a.core.RedisScan(r.Context(), r.PathValue("id"), v.Get("cursor"), v.Get("pattern"), count)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, keys)
}

// redisGet takes the key as a query parameter: keys are arbitrary bytes.
func (a *API) redisGet(w http.ResponseWriter, r *http.Request) {
	v := r.URL.Query()
	count, _ := strconv.Atoi(v.Get("count"))
	val, err := a.core.RedisGet(r.Context(), r.PathValue("id"), v.Get("key"), v.Get("cursor"), count)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, val)
}

func (a *API) dataConsole(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Query string `json:"query"`
		Write bool   `json:"write"`
	}
	if !decode(w, r, &body) {
		return
	}
	res, err := a.core.DataConsole(r.Context(), r.PathValue("id"), body.Query, body.Write)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}
