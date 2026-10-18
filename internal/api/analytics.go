package api

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/MatHoyer/kipitiny/internal/core"
)

func (a *API) getAnalytics(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.core.Analytics(r.Context()))
}

func (a *API) setAnalytics(w http.ResponseWriter, r *http.Request) {
	var body core.AnalyticsSettings
	if !decode(w, r, &body) {
		return
	}
	if r.URL.Query().Has("progress") {
		a.progress(w, r, func(ctx context.Context) (any, error) { return a.core.SetAnalytics(ctx, body) })
		return
	}
	v, err := a.core.SetAnalytics(r.Context(), body)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (a *API) deleteAnalyticsData(w http.ResponseWriter, r *http.Request) {
	if err := a.core.DeleteAnalyticsData(r.Context()); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// analyticsQuery reads from and to (unix seconds, default the last 24
// hours, to included), interval (seconds, default an hour), offset (seconds
// east of UTC, for day buckets) and filter (<breakdown>:<value>, repeated).
func analyticsQuery(r *http.Request) core.AnalyticsQuery {
	v := r.URL.Query()
	num := func(k string, def int64) int64 {
		n, err := strconv.ParseInt(v.Get(k), 10, 64)
		if err != nil {
			return def
		}
		return n
	}
	now := time.Now().Unix() + 1 // to is exclusive: this second's visits count
	return core.AnalyticsQuery{
		From:     time.Unix(num("from", now-86400), 0),
		To:       time.Unix(num("to", now), 0),
		Interval: int(num("interval", 3600)),
		Offset:   int(num("offset", 0)),
		Filters:  v["filter"],
	}
}

func (a *API) serviceAnalytics(w http.ResponseWriter, r *http.Request) {
	res, err := a.core.ServiceAnalytics(r.Context(), r.PathValue("id"), analyticsQuery(r))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// serviceVisits takes the range and filters of analyticsQuery, and before
// (a visit id) and limit to page.
func (a *API) serviceVisits(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	res, err := a.core.ServiceVisits(r.Context(), r.PathValue("id"), analyticsQuery(r), r.URL.Query().Get("before"), limit)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (a *API) serviceVisit(w http.ResponseWriter, r *http.Request) {
	res, err := a.core.ServiceVisit(r.Context(), r.PathValue("id"), r.PathValue("visit"))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}
