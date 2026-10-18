package analytics

import (
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

//go:embed tracker.js
var tracker []byte

// Hit is what the tracker sends.
type Hit struct {
	URL      string         `json:"u"`
	Referrer string         `json:"r"`
	Title    string         `json:"t"`
	Screen   string         `json:"s"`
	Language string         `json:"l"`
	Name     string         `json:"n"`
	Props    map[string]any `json:"p"`
	// Identity is what the page passed to kipitiny.identify.
	Identity string `json:"i"`
}

// maxHit caps a hit's body.
const maxHit = 8 << 10

// Public serves the tracker script and collects hits, on the analytics
// domain. It sets no cookie and answers any origin: hits are only kept for
// tracked hostnames.
func Public(st *Store, log *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /a.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Write(tracker)
	})
	mux.HandleFunc("OPTIONS /e", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /e", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		var h Hit
		if err := json.NewDecoder(io.LimitReader(r.Body, maxHit)).Decode(&h); err != nil {
			http.Error(w, "bad hit", http.StatusBadRequest)
			return
		}
		e, ok := eventOf(h, r)
		if ok && st.tracked(e.Hostname) {
			if err := st.Record(r.Context(), e); err != nil {
				log.Error("record hit", "err", err)
			}
		}
		// Same answer either way: a page can't probe what is tracked.
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {})
	return mux
}

var (
	screenRe  = regexp.MustCompile(`^\d{2,5}x\d{2,5}$`)
	countryRe = regexp.MustCompile(`^[A-Z]{2}$`)
)

func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "")
}

// eventOf prepares a hit. false for bots and hits without a page URL.
func eventOf(h Hit, r *http.Request) (Event, bool) {
	ua := r.UserAgent()
	u, err := url.Parse(h.URL)
	if err != nil || u.Hostname() == "" || isBot(ua) || len(h.Name) > 64 {
		return Event{}, false
	}
	host := strings.ToLower(u.Hostname())
	e := Event{
		Hostname: host, Name: h.Name, Path: cut(u.Path, 512), Title: cut(h.Title, 256),
		IP: clientIP(r), UserAgent: ua,
		Browser: first(ua, browsers), OS: first(ua, systems),
		Language: cut(strings.ToLower(h.Language), 35), Identity: cut(h.Identity, 128),
	}
	if e.Path == "" {
		e.Path = "/"
	}
	if screenRe.MatchString(h.Screen) {
		e.Screen = h.Screen
	}
	w, _ := strconv.Atoi(strings.Split(e.Screen, "x")[0])
	e.Device = device(ua, w)
	if c := r.Header.Get("CF-IPCountry"); countryRe.MatchString(c) && c != "XX" {
		e.Country = c
	}
	if ref, err := url.Parse(h.Referrer); err == nil && ref.Hostname() != "" && !strings.EqualFold(ref.Hostname(), host) {
		e.Referrer = strings.TrimPrefix(strings.ToLower(ref.Hostname()), "www.")
	}
	// Query values may be tokens (reset links, sessions): only the keys,
	// but UTM's values.
	q := u.Query()
	e.UTMSource, e.UTMMedium, e.UTMCampaign = cut(q.Get("utm_source"), 128), cut(q.Get("utm_medium"), 128), cut(q.Get("utm_campaign"), 128)
	e.UTMTerm, e.UTMContent = cut(q.Get("utm_term"), 128), cut(q.Get("utm_content"), 128)
	var keys []string
	for _, kv := range strings.Split(u.RawQuery, "&") {
		k, _, _ := strings.Cut(kv, "=")
		if k, err := url.QueryUnescape(k); err == nil && k != "" && !strings.HasPrefix(k, "utm_") {
			keys = append(keys, k)
		}
	}
	e.Query = cut(strings.Join(keys, "&"), 256)
	if h.Name != "" && len(h.Props) > 0 {
		e.Props = props(h.Props)
	}
	return e, true
}

// props keeps up to 20 scalar props, values cut to 128 bytes.
func props(in map[string]any) string {
	out := map[string]any{}
	for k, v := range in {
		if len(out) == 20 || len(k) > 64 {
			break
		}
		switch v := v.(type) {
		case string:
			out[k] = cut(v, 128)
		case float64, bool:
			out[k] = v
		}
	}
	if len(out) == 0 {
		return ""
	}
	b, _ := json.Marshal(out)
	return string(b)
}

// clientIP is the visitor's address as Traefik passes it on: Cloudflare's
// header when behind it, else Traefik's X-Real-Ip. Only hashed: a spoofed
// one at worst splits a visitor in two.
func clientIP(r *http.Request) string {
	for _, h := range []string{"CF-Connecting-IP", "X-Real-Ip"} {
		if v := r.Header.Get(h); v != "" {
			return v
		}
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	return host
}

// Private answers the manager, with token as a bearer: on a port no
// Traefik router reaches, but other containers of the proxy network could.
func Private(st *Store, token string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /api/sites", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Hostnames []string `json:"hostnames"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := st.SetSites(r.Context(), in.Hostnames); err != nil {
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
		vs, err := st.Visits(r.Context(), q, r.URL.Query().Get("before"), limit)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(vs)
	})
	mux.HandleFunc("GET /api/visits/{id}", func(w http.ResponseWriter, r *http.Request) {
		d, err := st.Visit(r.Context(), r.URL.Query()["host"], r.PathValue("id"))
		if errors.Is(err, ErrNotFound) {
			http.Error(w, "no such visit", http.StatusNotFound)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(d)
	})
	mux.HandleFunc("GET /api/stats", func(w http.ResponseWriter, r *http.Request) {
		q, err := parseQuery(r.URL.Query())
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s, err := st.Stats(r.Context(), q)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(s)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if token == "" || subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func parseQuery(v url.Values) (Query, error) {
	q := Query{Hosts: v["host"]}
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
	if q.Filters, err = ParseFilters(v["filter"]); err != nil {
		return q, err
	}
	if to <= from || to-from > 2*366*86400 || iv < 60 || (to-from)/iv > 1000 {
		return q, fmt.Errorf("range: from < to, at most 2 years and 1000 buckets of at least a minute")
	}
	return q, nil
}

// Ports the server listens on, in its container.
const (
	PublicPort  = 8080
	PrivatePort = 8081
)
