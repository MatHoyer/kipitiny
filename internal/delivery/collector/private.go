package collector

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/kipit-labs/kipitiny/internal/domain/analytics"
	"github.com/kipit-labs/kipitiny/internal/usecase/ingest"
)

// Private answers the manager, with token as a bearer: on a port no
// Traefik router reaches, but other containers of the proxy network could.
func Private(svc *ingest.Service, token string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /api/sites", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Hostnames []string                   `json:"hostnames"`
			Paths     map[string]analytics.Paths `json:"paths"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := svc.SetSites(r.Context(), in.Hostnames, in.Paths); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /api/visits", func(w http.ResponseWriter, r *http.Request) {
		q, err := parseQuery(r.URL.Query())
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		vs, err := svc.Visits(r.Context(), q, r.URL.Query().Get("before"), limit)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(vs)
	})
	mux.HandleFunc("GET /api/visits/{id}", func(w http.ResponseWriter, r *http.Request) {
		d, err := svc.Visit(r.Context(), r.URL.Query()["host"], r.PathValue("id"))
		if errors.Is(err, analytics.ErrNotFound) {
			http.Error(w, "no such visit", http.StatusNotFound)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(d)
	})
	mux.HandleFunc("GET /api/stats", func(w http.ResponseWriter, r *http.Request) {
		q, err := parseQuery(r.URL.Query())
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s, err := svc.Stats(r.Context(), q)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(s)
	})
	mux.HandleFunc("GET /api/funnel", func(w http.ResponseWriter, r *http.Request) {
		q, err := parseQuery(r.URL.Query())
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		steps, err := analytics.ParseSteps(r.URL.Query()["step"])
		if err != nil {
			reply(w, nil, fmt.Errorf("%w: %w", analytics.ErrInvalid, err))
			return
		}
		fs, err := svc.Funnel(r.Context(), q, steps)
		reply(w, fs, err)
	})
	mux.HandleFunc("GET /api/trackers", func(w http.ResponseWriter, r *http.Request) {
		ts, err := svc.Trackers(r.Context())
		reply(w, ts, err)
	})
	mux.HandleFunc("GET /api/trackers/{id}", func(w http.ResponseWriter, r *http.Request) {
		t, err := svc.Tracker(r.Context(), r.PathValue("id"))
		reply(w, t, err)
	})
	mux.HandleFunc("POST /api/trackers", func(w http.ResponseWriter, r *http.Request) {
		var t analytics.Tracker
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&t); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		t, err := svc.CreateTracker(r.Context(), t)
		reply(w, t, err)
	})
	mux.HandleFunc("PUT /api/trackers/{id}", func(w http.ResponseWriter, r *http.Request) {
		var t analytics.Tracker
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&t); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		t.ID = r.PathValue("id")
		t, err := svc.UpdateTracker(r.Context(), t)
		reply(w, t, err)
	})
	mux.HandleFunc("DELETE /api/trackers/{id}", func(w http.ResponseWriter, r *http.Request) {
		reply(w, nil, svc.DeleteTracker(r.Context(), r.PathValue("id")))
	})
	errorRoutes(mux, svc)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if token == "" || subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// reply writes v as JSON (nothing when nil), or err: 404 and 400 for the
// store's ErrNotFound and ErrInvalid, with ErrInvalid's reason only.
func reply(w http.ResponseWriter, v any, err error) {
	switch {
	case errors.Is(err, analytics.ErrNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, analytics.ErrInvalid):
		http.Error(w, strings.TrimPrefix(err.Error(), analytics.ErrInvalid.Error()+": "), http.StatusBadRequest)
	case err != nil:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	case v == nil:
		w.WriteHeader(http.StatusNoContent)
	default:
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
}

func parseQuery(v url.Values) (analytics.Query, error) {
	q := analytics.Query{Hosts: v["host"]}
	num := func(k string) (int64, error) {
		n, err := strconv.ParseInt(v.Get(k), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("%s: %w", k, err)
		}
		return n, nil
	}
	from, err := num("from")
	if err != nil {
		return q, err
	}
	to, err := num("to")
	if err != nil {
		return q, err
	}
	iv, err := num("interval")
	if err != nil {
		return q, err
	}
	q.From, q.To, q.Interval = time.Unix(from, 0), time.Unix(to, 0), time.Duration(iv)*time.Second
	if o := v.Get("offset"); o != "" {
		off, err := strconv.Atoi(o)
		if err != nil {
			return q, fmt.Errorf("offset: %w", err)
		}
		q.Offset = off
	}
	if q.Filters, err = analytics.ParseFilters(v["filter"]); err != nil {
		return q, err
	}
	if to <= from || to-from > 2*366*86400 || iv < 60 || (to-from)/iv > 1000 {
		return q, fmt.Errorf("range: from < to, at most 2 years and 1000 buckets of at least a minute")
	}
	return q, nil
}
