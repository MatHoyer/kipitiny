// Package api exposes the core layer over JSON/HTTP.
package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/MatHoyer/kipitiny/internal/core"
	"github.com/MatHoyer/kipitiny/internal/store"
)

type API struct {
	core    *core.Core
	log     *slog.Logger
	limiter *failLimiter
	handler http.Handler
}

func New(c *core.Core, log *slog.Logger) *API {
	a := &API{core: c, log: log, limiter: newFailLimiter(10, 15*time.Minute)}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", a.health)
	mux.HandleFunc("GET /api/auth/state", a.authState)
	mux.HandleFunc("POST /api/auth/setup", a.setup)
	mux.HandleFunc("POST /api/auth/login", a.login)
	mux.HandleFunc("POST /api/auth/logout", a.logout)
	mux.HandleFunc("GET /api/status", a.status)
	mux.HandleFunc("GET /api/projects", a.listProjects)
	mux.HandleFunc("POST /api/projects", a.createProject)
	mux.HandleFunc("GET /api/projects/{id}", a.getProject)
	mux.HandleFunc("DELETE /api/projects/{id}", a.deleteProject)
	mux.HandleFunc("GET /api/projects/{id}/services", a.listServices)
	mux.HandleFunc("POST /api/projects/{id}/services", a.createService)
	mux.HandleFunc("GET /api/services/{id}", a.getService)
	mux.HandleFunc("PATCH /api/services/{id}", a.updateService)
	mux.HandleFunc("DELETE /api/services/{id}", a.deleteService)
	mux.HandleFunc("POST /api/services/{id}/deploy", a.deployService)
	mux.HandleFunc("POST /api/services/{id}/rollback", a.rollbackService)
	mux.HandleFunc("POST /api/services/{id}/{action}", a.serviceAction)
	mux.HandleFunc("GET /api/services/{id}/deployments", a.listDeployments)
	mux.HandleFunc("GET /api/services/{id}/logs", a.streamLogs)
	mux.HandleFunc("GET /api/services/{id}/connection", a.connection)
	mux.HandleFunc("GET /api/services/{id}/webhook", a.webhook)
	mux.HandleFunc("POST /api/hooks/{id}", a.hook)
	mux.HandleFunc("GET /api/services/{id}/backups", a.listServiceBackups)
	mux.HandleFunc("POST /api/services/{id}/backups", a.createBackup)
	mux.HandleFunc("GET /api/services/{id}/restores", a.listRestores)
	mux.HandleFunc("GET /api/services/{id}/schedules", a.listSchedules)
	mux.HandleFunc("POST /api/services/{id}/schedules", a.createSchedule)
	mux.HandleFunc("PUT /api/schedules/{id}", a.updateSchedule)
	mux.HandleFunc("DELETE /api/schedules/{id}", a.deleteSchedule)
	mux.HandleFunc("POST /api/projects/{id}/backups", a.backupProject)
	mux.HandleFunc("GET /api/backups", a.listBackups)
	mux.HandleFunc("DELETE /api/backups/{id}", a.deleteBackup)
	mux.HandleFunc("GET /api/backups/{id}/download", a.downloadBackup)
	mux.HandleFunc("POST /api/backups/{id}/restore", a.restoreBackup)
	mux.HandleFunc("POST /api/backups/{id}/verify", a.verifyBackup)
	mux.HandleFunc("GET /api/backup-targets", a.listTargets)
	mux.HandleFunc("POST /api/backup-targets", a.createTarget)
	mux.HandleFunc("PUT /api/backup-targets/{id}", a.updateTarget)
	mux.HandleFunc("DELETE /api/backup-targets/{id}", a.deleteTarget)
	mux.HandleFunc("GET /api/backup-targets/{id}/key", a.targetKey)
	mux.HandleFunc("POST /api/manager/backups", a.backupManager)
	mux.HandleFunc("GET /api/deployments/{id}", a.getDeployment)
	mux.HandleFunc("GET /api/deployments/{id}/log", a.deploymentLog)
	mux.HandleFunc("GET /api/servers", a.listServers)
	mux.HandleFunc("POST /api/servers", a.createServer)
	mux.HandleFunc("PUT /api/servers/{id}", a.updateServer)
	mux.HandleFunc("DELETE /api/servers/{id}", a.deleteServer)
	mux.HandleFunc("GET /api/ssh-key", a.sshKey)
	mux.HandleFunc("GET /api/tokens", a.listTokens)
	mux.HandleFunc("POST /api/tokens", a.createToken)
	mux.HandleFunc("DELETE /api/tokens/{id}", a.deleteToken)
	mux.HandleFunc("GET /api/audit", a.listAudit)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not found")
	})
	a.handler = a.protect(mux)
	return a
}

func (a *API) ServeHTTP(w http.ResponseWriter, r *http.Request) { a.handler.ServeHTTP(w, r) }

func (a *API) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (a *API) status(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.core.Status(r.Context()))
}

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

func (a *API) deleteProject(w http.ResponseWriter, r *http.Request) {
	if err := a.core.DeleteProject(r.Context(), r.PathValue("id"), r.URL.Query().Get("confirm")); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, core.ErrUnauthorized):
		writeError(w, http.StatusUnauthorized, err.Error())
	case errors.Is(err, core.ErrForbidden):
		writeError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, core.ErrInvalid):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, "already exists (name or domain taken)")
	case errors.Is(err, core.ErrBusy):
		writeError(w, http.StatusConflict, "another operation is in progress for this service")
	default:
		a.log.Error("request failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
