package api

import (
	"encoding/json"
	"net/http"

	"github.com/MatHoyer/kipitiny/internal/core"
)

// brandingLogo serves the manager's logo, public for the sign-in page and
// the favicon. An SVG opened directly runs no script: the sandbox CSP
// applies, and the UI only draws it with <img>.
func (a *API) brandingLogo(w http.ResponseWriter, r *http.Request) {
	data, typ, err := a.core.BrandingLogo(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", typ)
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	if r.URL.Query().Get("v") != "" {
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		h.Set("Cache-Control", "no-cache")
	}
	_, _ = w.Write(data)
}

// manifest is public: browsers fetch it without credentials.
func (a *API) manifest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Type", "application/manifest+json")
	_ = json.NewEncoder(w).Encode(a.core.Manifest(r.Context()))
}

func (a *API) setBranding(w http.ResponseWriter, r *http.Request) {
	var body core.BrandingInput
	if !decode(w, r, &body) {
		return
	}
	b, err := a.core.SetBranding(r.Context(), body)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, b)
}

func (a *API) resetBranding(w http.ResponseWriter, r *http.Request) {
	if err := a.core.ResetBranding(r.Context()); err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a.core.Branding(r.Context()))
}
