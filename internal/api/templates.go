package api

import (
	"net/http"

	"github.com/MatHoyer/kipitiny/internal/core"
)

func (a *API) listTemplates(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.core.ListTemplates())
}

// installTemplate installs a template into a project, or a new one; with
// dryRun, only returns what it would create.
func (a *API) installTemplate(w http.ResponseWriter, r *http.Request) {
	var body core.TemplateInstall
	if !decode(w, r, &body) {
		return
	}
	res, err := a.core.InstallTemplate(r.Context(), r.PathValue("id"), body)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}
