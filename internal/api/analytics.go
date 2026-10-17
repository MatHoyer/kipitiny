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

// serviceAnalytics takes from and to (unix seconds, default the last 24
// hours, to included), interval (seconds, default an hour) and offset (seconds east of
// UTC, for day buckets).
func (a *API) serviceAnalytics(w http.ResponseWriter, r *http.Request) {
	v := r.URL.Query()
	num := func(k string, def int64) int64 {
		n, err := strconv.ParseInt(v.Get(k), 10, 64)
		if err != nil {
			return def
		}
		return n
	}
	now := time.Now().Unix() + 1 // to is exclusive: this second's visits count
	q := core.AnalyticsQuery{
		From:     time.Unix(num("from", now-86400), 0),
		To:       time.Unix(num("to", now), 0),
		Interval: int(num("interval", 3600)),
		Offset:   int(num("offset", 0)),
	}
	res, err := a.core.ServiceAnalytics(r.Context(), r.PathValue("id"), q)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}
