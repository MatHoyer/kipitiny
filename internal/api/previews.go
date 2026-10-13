package api

import (
	"net/http"

	"github.com/MatHoyer/kipitiny/internal/core"
)

func (a *API) listPreviews(w http.ResponseWriter, r *http.Request) {
	ps, err := a.core.ListPreviews(r.Context(), r.PathValue("id"))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ps)
}

// deployPreview creates or updates a preview; changed services deploy in
// the background (the CLI waits for them).
func (a *API) deployPreview(w http.ResponseWriter, r *http.Request) {
	var opts core.PreviewOptions
	if !decode(w, r, &opts) {
		return
	}
	v, err := a.core.DeployPreview(r.Context(), r.PathValue("id"), r.PathValue("name"), opts)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, v)
}

func (a *API) closePreview(w http.ResponseWriter, r *http.Request) {
	if err := a.core.ClosePreview(r.Context(), r.PathValue("id"), r.PathValue("name")); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) previewSettings(w http.ResponseWriter, r *http.Request) {
	ps, err := a.core.GetPreviewSettings(r.Context(), r.PathValue("id"))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ps)
}

func (a *API) setPreviewSettings(w http.ResponseWriter, r *http.Request) {
	var in core.PreviewSettingsInput
	if !decode(w, r, &in) {
		return
	}
	ps, err := a.core.SetPreviewSettings(r.Context(), r.PathValue("id"), in)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ps)
}
