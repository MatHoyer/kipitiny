package api

import (
	"net/http"

	"github.com/MatHoyer/kipitiny/internal/core"
)

func (a *API) canvasLayout(w http.ResponseWriter, r *http.Request) {
	ps, err := a.core.CanvasLayout(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"positions": ps})
}

func (a *API) saveCanvasLayout(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Positions map[string]core.Point `json:"positions"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := a.core.SaveCanvasLayout(r.Context(), in.Positions); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) resetCanvasLayout(w http.ResponseWriter, r *http.Request) {
	if err := a.core.ResetCanvasLayout(r.Context()); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
