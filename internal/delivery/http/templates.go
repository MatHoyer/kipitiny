package http

import (
	"net/http"

	"github.com/kipit-labs/kipitiny/internal/usecase/projects"
)

func (a *API) listTemplates(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, a.core.ListTemplates())
}

// templateLogos serves the logos templates bring, drawn by the UI for
// services that use them.
func (a *API) templateLogos(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, a.core.TemplateLogos())
}

// installTemplate installs a template into a project, or a new one; with
// dryRun, only returns what it would create.
func (a *API) installTemplate(w http.ResponseWriter, r *http.Request) {
	var body projects.TemplateInstall
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
