package api

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/MatHoyer/kipitiny/internal/core"
)

const (
	// The default page has no scripts and loads nothing.
	pageCSP = "sandbox; default-src 'none'; style-src 'unsafe-inline'; img-src data:"
	// A custom page may use scripts and other origins, but never the
	// manager's: sandboxed, it has an opaque origin even when opened on the
	// manager's domain.
	customPageCSP = "sandbox allow-scripts allow-forms allow-popups allow-popups-to-escape-sandbox"
)

// Pages serves the maintenance pages Traefik shows for apps that can't
// answer, and the configuration Traefik routes them with. Public: visitors
// of any app reach it through Traefik, whatever the request (a form post to
// an app that is down too).
func Pages(c *core.Core, log *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+core.PagesPath+"traefik/{server}", func(w http.ResponseWriter, r *http.Request) {
		token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		b, err := c.TraefikConfig(r.Context(), r.PathValue("server"), token)
		if errors.Is(err, core.ErrUnauthorized) {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if err != nil {
			// Traefik keeps its last configuration meanwhile.
			log.Error("traefik configuration", "err", err)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(b)
	})
	mux.HandleFunc(core.PagesPath+"down", func(w http.ResponseWriter, r *http.Request) {
		writePage(w, r, c.MaintenancePage(r.Context(), "", r.URL.Query().Get("url")))
	})
	mux.HandleFunc(core.PagesPath+"{id}", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "starting" { // to see it, without waking the app
			writePage(w, r, c.StartingPage(r.Context(), r.PathValue("id")))
			return
		}
		writePage(w, r, c.MaintenancePage(r.Context(), r.PathValue("id"), ""))
	})
	return mux
}

// writePage answers 503 with Retry-After, so crawlers come back later
// instead of indexing the page, and uptime checks see the app down. An app
// waking from sleep asks to retry in seconds, and clients that don't take
// HTML (API calls) get only that, no page.
func writePage(w http.ResponseWriter, r *http.Request, p core.Page) {
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("X-Robots-Tag", "noindex")
	if p.Starting {
		h.Set("Retry-After", "5")
		h.Set("Refresh", "3")
		if !acceptsHTML(r) {
			h.Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			if r.Method != http.MethodHead {
				_, _ = w.Write([]byte(`{"error":"starting","message":"the app is starting, retry in a few seconds"}` + "\n"))
			}
			return
		}
	} else {
		h.Set("Retry-After", "300")
	}
	h.Set("Content-Type", "text/html; charset=utf-8")
	if p.Custom {
		h.Set("Content-Security-Policy", customPageCSP)
	} else {
		h.Set("Content-Security-Policy", pageCSP)
	}
	w.WriteHeader(http.StatusServiceUnavailable)
	if r.Method != http.MethodHead {
		_, _ = w.Write(p.Body)
	}
}

// acceptsHTML reports whether the client takes an HTML page: browsers say
// so; fetch() and API clients usually send */* or JSON.
func acceptsHTML(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}
