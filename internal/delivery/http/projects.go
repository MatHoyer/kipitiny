package http

import (
	"net/http"

	"github.com/kipit-labs/kipitiny/internal/usecase/deploy"

	"github.com/kipit-labs/kipitiny/internal/usecase/projects"
)

func (a *API) listProjects(w http.ResponseWriter, r *http.Request) {
	ps, err := a.core.ListProjects(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ps)
}

func (a *API) createProject(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name     string `json:"name"`
		ServerID string `json:"serverId"`
	}
	if !decode(w, r, &body) {
		return
	}
	p, err := a.core.CreateProject(r.Context(), body.Name, body.ServerID)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

func (a *API) getProject(w http.ResponseWriter, r *http.Request) {
	p, err := a.core.GetProject(r.Context(), r.PathValue("id"))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// setProjectEnv replaces the shared variables and secrets; masked secrets
// are kept.
func (a *API) setProjectEnv(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Env     map[string]string `json:"env"`
		Secrets []string          `json:"secrets"`
	}
	if !decode(w, r, &body) {
		return
	}
	p, err := a.core.SetProjectEnv(r.Context(), r.PathValue("id"), body.Env, body.Secrets)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (a *API) renameProject(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &body) {
		return
	}
	p, err := a.core.RenameProject(r.Context(), r.PathValue("id"), body.Name)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (a *API) setProjectTags(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Tags []string `json:"tags"`
	}
	if !decode(w, r, &body) {
		return
	}
	p, err := a.core.SetProjectTags(r.Context(), r.PathValue("id"), body.Tags)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (a *API) setServiceTags(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Tags []string `json:"tags"`
	}
	if !decode(w, r, &body) {
		return
	}
	v, err := a.core.SetServiceTags(r.Context(), r.PathValue("id"), body.Tags)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (a *API) listTags(w http.ResponseWriter, r *http.Request) {
	tags, err := a.core.ListTags(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tags": tags, "colors": projects.TagColors})
}

func (a *API) setTagColor(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Color string `json:"color"`
	}
	if !decode(w, r, &body) {
		return
	}
	t, err := a.core.SetTagColor(r.Context(), r.PathValue("name"), body.Color)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (a *API) setProjectSleep(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SleepAfterSeconds int `json:"sleepAfterSeconds"`
	}
	if !decode(w, r, &body) {
		return
	}
	p, err := a.core.SetProjectSleep(r.Context(), r.PathValue("id"), body.SleepAfterSeconds)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (a *API) setMaintenance(w http.ResponseWriter, r *http.Request) {
	var in deploy.MaintenanceInput
	if !decode(w, r, &in) {
		return
	}
	v, err := a.core.SetMaintenance(r.Context(), r.PathValue("id"), in)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (a *API) deleteProject(w http.ResponseWriter, r *http.Request) {
	if err := a.core.DeleteProject(r.Context(), r.PathValue("id"), r.URL.Query().Get("confirm")); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
