// Package collector is the analytics server's HTTP side: the tracker
// script and hits, pixels, links and SDK errors on the public port, the
// manager's API on the private one.
package collector

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"

	"github.com/kipit-labs/kipitiny/internal/domain/analytics"
	"github.com/kipit-labs/kipitiny/internal/usecase/ingest"
)

//go:embed tracker.js
var tracker []byte

// maxHit caps a hit's body.
const maxHit = 8 << 10

// Public serves the tracker script and collects hits, on the analytics
// domain. It sets no cookie and answers any origin: hits are only kept for
// tracked hostnames.
func Public(svc *ingest.Service, log *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	script := func(w http.ResponseWriter, js []byte) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		_, _ = w.Write(js)
	}
	preflight := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.WriteHeader(http.StatusNoContent)
	}
	hit := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		var h ingest.Hit
		if err := json.NewDecoder(io.LimitReader(r.Body, maxHit)).Decode(&h); err != nil {
			http.Error(w, "bad hit", http.StatusBadRequest)
			return
		}
		if err := svc.Hit(r.Context(), h, visitor(r)); err != nil {
			log.Error("record hit", "err", err)
		}
		// Same answer either way: a page can't probe what is tracked.
		w.WriteHeader(http.StatusNoContent)
	}
	mux.HandleFunc("GET /a.js", func(w http.ResponseWriter, _ *http.Request) { script(w, tracker) })
	mux.HandleFunc("OPTIONS /e", preflight)
	mux.HandleFunc("POST /e", hit)
	mux.HandleFunc("GET /p/{file}", func(w http.ResponseWriter, r *http.Request) {
		if slug, ok := strings.CutSuffix(r.PathValue("file"), ".gif"); ok {
			if t, ok := svc.TrackerBySlug(analytics.KindPixel, slug); ok {
				recordTracker(svc, log, t, r)
			}
		}
		// The same GIF whatever the slug, never cached so each load counts.
		w.Header().Set("Content-Type", "image/gif")
		w.Header().Set("Cache-Control", "no-store, max-age=0")
		_, _ = w.Write(pixelGIF)
	})
	mux.HandleFunc("GET /l/{slug}", func(w http.ResponseWriter, r *http.Request) {
		t, ok := svc.TrackerBySlug(analytics.KindLink, r.PathValue("slug"))
		if !ok {
			http.NotFound(w, r)
			return
		}
		recordTracker(svc, log, t, r)
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, t.Target, http.StatusFound)
	})
	mux.HandleFunc("POST /api/{project}/envelope/", ingestErrors(svc, log, true))
	mux.HandleFunc("POST /api/{project}/store/", ingestErrors(svc, log, false))
	mux.HandleFunc("OPTIONS /api/{project}/{kind}/", func(w http.ResponseWriter, _ *http.Request) {
		errorsCORS(w)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /healthz", func(http.ResponseWriter, *http.Request) {})
	// A site's own paths, on its own domain (Traefik routes only those
	// there); anything else falls through to the analytics domain's.
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		if p, ok := svc.SitePaths(strings.ToLower(host)); ok {
			switch {
			case r.URL.Path == p.Script && r.Method == http.MethodGet:
				script(w, bytes.Replace(tracker, []byte(`"/e"`), []byte(`"`+p.Hit+`"`), 1))
				return
			case r.URL.Path == p.Hit && r.Method == http.MethodPost:
				hit(w, r)
				return
			case r.URL.Path == p.Hit && r.Method == http.MethodOptions:
				preflight(w, r)
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}

// pixelGIF is a transparent 1x1 GIF.
var pixelGIF = []byte("GIF89a\x01\x00\x01\x00\x80\x00\x00\x00\x00\x00\xff\xff\xff!\xf9\x04\x01\x00\x00\x00\x00,\x00\x00\x00\x00\x01\x00\x01\x00\x00\x02\x02D\x01\x00;")

// visitor is what r tells of who sent it.
func visitor(r *http.Request) ingest.Visitor {
	return ingest.Visitor{
		UserAgent: r.UserAgent(), IP: clientIP(r), Country: r.Header.Get("CF-IPCountry"),
		Language: r.Header.Get("Accept-Language"), Referrer: r.Referer(),
	}
}

// recordTracker counts a pixel's view or a link's click.
func recordTracker(svc *ingest.Service, log *slog.Logger, t analytics.Tracker, r *http.Request) {
	if r.Method != http.MethodGet {
		return
	}
	if err := svc.TrackerHit(r.Context(), t, visitor(r), r.URL.Query()); err != nil {
		log.Error("record "+t.Kind, "err", err)
	}
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
