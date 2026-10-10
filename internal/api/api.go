// Package api exposes the core layer over JSON/HTTP.
package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/MatHoyer/kipitiny/internal/core"
	"github.com/MatHoyer/kipitiny/internal/store"
)

type API struct {
	core    *core.Core
	log     *slog.Logger
	limiter *failLimiter
	// begins counts unauthenticated passkey sign-in starts per IP.
	begins  *failLimiter
	handler http.Handler
}

func New(c *core.Core, log *slog.Logger) *API {
	a := &API{core: c, log: log, limiter: newFailLimiter(10, 15*time.Minute), begins: newFailLimiter(30, time.Minute)}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", a.health)
	mux.HandleFunc("GET /api/auth/state", a.authState)
	mux.HandleFunc("POST /api/auth/setup", a.setup)
	mux.HandleFunc("POST /api/auth/login", a.login)
	mux.HandleFunc("POST /api/auth/login/mfa", a.loginMFA)
	mux.HandleFunc("POST /api/auth/passkey/begin", a.beginPasskeyLogin)
	mux.HandleFunc("POST /api/auth/passkey/finish", a.finishPasskeyLogin)
	mux.HandleFunc("POST /api/auth/logout", a.logout)
	mux.HandleFunc("GET /api/account", a.account)
	mux.HandleFunc("PUT /api/account/password", a.changePassword)
	mux.HandleFunc("POST /api/account/totp", a.beginTOTP)
	mux.HandleFunc("POST /api/account/totp/enable", a.enableTOTP)
	mux.HandleFunc("POST /api/account/totp/disable", a.disableTOTP)
	mux.HandleFunc("POST /api/account/recovery-codes", a.regenerateRecoveryCodes)
	mux.HandleFunc("POST /api/account/passkeys/begin", a.beginPasskeyRegistration)
	mux.HandleFunc("POST /api/account/passkeys", a.finishPasskeyRegistration)
	mux.HandleFunc("DELETE /api/account/passkeys/{id}", a.deletePasskey)
	mux.HandleFunc("GET /api/status", a.status)
	mux.HandleFunc("POST /api/update", a.applyUpdate)
	mux.HandleFunc("POST /api/update/check", a.checkUpdate)
	mux.HandleFunc("GET /api/projects", a.listProjects)
	mux.HandleFunc("POST /api/projects", a.createProject)
	mux.HandleFunc("GET /api/projects/{id}", a.getProject)
	mux.HandleFunc("DELETE /api/projects/{id}", a.deleteProject)
	mux.HandleFunc("PUT /api/projects/{id}/env", a.setProjectEnv)
	mux.HandleFunc("PUT /api/projects/{id}/name", a.renameProject)
	mux.HandleFunc("GET /api/projects/{id}/compose", a.exportCompose)
	mux.HandleFunc("POST /api/projects/{id}/compose", a.applyCompose)
	mux.HandleFunc("POST /api/projects/{id}/export", a.exportBundle)
	mux.HandleFunc("GET /api/projects/{id}/git", a.projectGit)
	mux.HandleFunc("GET /api/templates", a.listTemplates)
	mux.HandleFunc("POST /api/templates/{id}/install", a.installTemplate)
	mux.HandleFunc("PUT /api/projects/{id}/git", a.linkProjectGit)
	mux.HandleFunc("DELETE /api/projects/{id}/git", a.unlinkProjectGit)
	mux.HandleFunc("POST /api/projects/{id}/git/preview", a.previewProjectGit)
	mux.HandleFunc("POST /api/projects/{id}/git/sync", a.syncProjectGit)
	mux.HandleFunc("POST /api/hooks/projects/{id}", a.gitHook)
	mux.HandleFunc("GET /api/projects/{id}/topology", a.topology)
	mux.HandleFunc("GET /api/topology", a.topology)
	mux.HandleFunc("GET /api/projects/{id}/services", a.listServices)
	mux.HandleFunc("POST /api/projects/{id}/services", a.createService)
	mux.HandleFunc("GET /api/services/{id}", a.getService)
	mux.HandleFunc("PATCH /api/services/{id}", a.updateService)
	mux.HandleFunc("DELETE /api/services/{id}", a.deleteService)
	mux.HandleFunc("PUT /api/services/{id}/name", a.renameService)
	mux.HandleFunc("PUT /api/services/{id}/networks", a.setServiceNetworks)
	mux.HandleFunc("POST /api/services/{id}/deploy", a.deployService)
	mux.HandleFunc("POST /api/services/{id}/rollback", a.rollbackService)
	mux.HandleFunc("POST /api/services/{id}/{action}", a.serviceAction)
	mux.HandleFunc("GET /api/services/{id}/deployments", a.listDeployments)
	mux.HandleFunc("GET /api/services/{id}/logs", a.streamLogs)
	mux.HandleFunc("GET /api/services/{id}/stats", a.serviceStats)
	mux.HandleFunc("GET /api/services/{id}/uptime", a.uptime)
	mux.HandleFunc("PUT /api/services/{id}/uptime", a.setUptime)
	mux.HandleFunc("DELETE /api/services/{id}/uptime", a.deleteUptime)
	mux.HandleFunc("GET /api/stats", a.usage)
	mux.HandleFunc("GET /api/services/{id}/terminal", a.serviceTerminal)
	mux.HandleFunc("GET /api/services/{id}/connection", a.connection)
	mux.HandleFunc("GET /api/services/{id}/data/databases", a.pgDatabases)
	mux.HandleFunc("POST /api/services/{id}/data/databases", a.createPgDatabase)
	mux.HandleFunc("GET /api/services/{id}/data/tables", a.pgTables)
	mux.HandleFunc("GET /api/services/{id}/data/tables/{schema}/{table}", a.pgRows)
	mux.HandleFunc("GET /api/services/{id}/data/tables/{schema}/{table}/export", a.pgExport)
	mux.HandleFunc("GET /api/services/{id}/data/keys", a.redisScan)
	mux.HandleFunc("GET /api/services/{id}/data/key", a.redisGet)
	mux.HandleFunc("POST /api/services/{id}/data/console", a.dataConsole)
	mux.HandleFunc("GET /api/services/{id}/backups", a.listServiceBackups)
	mux.HandleFunc("POST /api/services/{id}/backups", a.createBackup)
	mux.HandleFunc("GET /api/services/{id}/restores", a.listRestores)
	mux.HandleFunc("GET /api/services/{id}/schedules", a.listSchedules)
	mux.HandleFunc("POST /api/services/{id}/schedules", a.createSchedule)
	mux.HandleFunc("PUT /api/schedules/{id}", a.updateSchedule)
	mux.HandleFunc("DELETE /api/schedules/{id}", a.deleteSchedule)
	mux.HandleFunc("POST /api/projects/{id}/backups", a.backupProject)
	mux.HandleFunc("GET /api/backups", a.listBackups)
	mux.HandleFunc("GET /api/backups/{id}", a.getBackup)
	mux.HandleFunc("DELETE /api/backups/{id}", a.deleteBackup)
	mux.HandleFunc("GET /api/backups/{id}/download", a.downloadBackup)
	mux.HandleFunc("POST /api/backups/{id}/restore", a.restoreBackup)
	mux.HandleFunc("POST /api/backups/{id}/verify", a.verifyBackup)
	mux.HandleFunc("GET /api/storage", a.listStorage)
	mux.HandleFunc("POST /api/storage", a.createStorage)
	mux.HandleFunc("PUT /api/storage/{id}", a.updateStorage)
	mux.HandleFunc("DELETE /api/storage/{id}", a.deleteStorage)
	mux.HandleFunc("GET /api/storage/{id}/key", a.storageKey)
	mux.HandleFunc("POST /api/storage/{id}/test", a.testStorage)
	mux.HandleFunc("POST /api/storage/{id}/encrypt", a.encryptStorage)
	mux.HandleFunc("POST /api/manager/backups", a.backupManager)
	mux.HandleFunc("GET /api/manager/schedules", a.listManagerSchedules)
	mux.HandleFunc("POST /api/manager/schedules", a.createManagerSchedule)
	mux.HandleFunc("GET /api/deployments/{id}", a.getDeployment)
	mux.HandleFunc("GET /api/deployments/{id}/log", a.deploymentLog)
	mux.HandleFunc("GET /api/servers", a.listServers)
	mux.HandleFunc("POST /api/servers", a.createServer)
	mux.HandleFunc("PUT /api/servers/{id}", a.updateServer)
	mux.HandleFunc("DELETE /api/servers/{id}", a.deleteServer)
	mux.HandleFunc("GET /api/servers/{id}/terminal", a.serverTerminal)
	mux.HandleFunc("GET /api/ssh-key", a.sshKey)
	mux.HandleFunc("GET /api/domains", a.listDomains)
	mux.HandleFunc("POST /api/domains", a.createDomain)
	mux.HandleFunc("PATCH /api/domains/{id}", a.updateDomain)
	mux.HandleFunc("DELETE /api/domains/{id}", a.deleteDomain)
	mux.HandleFunc("GET /api/cloudflare", a.cloudflareStatus)
	mux.HandleFunc("PUT /api/cloudflare", a.connectCloudflare)
	mux.HandleFunc("DELETE /api/cloudflare", a.disconnectCloudflare)
	mux.HandleFunc("POST /api/cloudflare/test", a.testCloudflare)
	mux.HandleFunc("PUT /api/servers/{id}/network", a.setServerNetwork)
	mux.HandleFunc("GET /api/networks", a.listNetworks)
	mux.HandleFunc("GET /api/canvas", a.canvasLayout)
	mux.HandleFunc("PUT /api/canvas", a.saveCanvasLayout)
	mux.HandleFunc("DELETE /api/canvas", a.resetCanvasLayout)
	mux.HandleFunc("POST /api/networks", a.createNetwork)
	mux.HandleFunc("DELETE /api/networks/{id}", a.deleteNetwork)
	mux.HandleFunc("GET /api/secret-providers", a.secretProviders)
	mux.HandleFunc("PUT /api/secret-providers/{id}", a.connectSecretProvider)
	mux.HandleFunc("DELETE /api/secret-providers/{id}", a.disconnectSecretProvider)
	mux.HandleFunc("GET /api/secret-providers/{id}/vaults", a.secretVaults)
	mux.HandleFunc("POST /api/secret-providers/{id}/test", a.testSecretProvider)
	mux.HandleFunc("GET /api/secret-providers/{id}/items", a.secretItems)
	mux.HandleFunc("GET /api/cleanup", a.getCleanup)
	mux.HandleFunc("PUT /api/cleanup", a.setCleanup)
	mux.HandleFunc("POST /api/cleanup/run", a.runCleanup)
	mux.HandleFunc("GET /api/git-providers", a.listGitProviders)
	mux.HandleFunc("POST /api/git-providers", a.createGitProvider)
	mux.HandleFunc("PUT /api/git-providers/{id}", a.updateGitProvider)
	mux.HandleFunc("DELETE /api/git-providers/{id}", a.deleteGitProvider)
	mux.HandleFunc("POST /api/git-providers/{id}/test", a.testGitProvider)
	mux.HandleFunc("POST /api/git-providers/{id}/authorize", a.authorizeGitProvider)
	mux.HandleFunc("GET /api/git-providers/{id}/repos", a.gitProviderRepos)
	mux.HandleFunc("GET /api/git-providers/{id}/branches", a.gitProviderBranches)
	mux.HandleFunc("POST /api/git-providers/github", a.startGitHubApp)
	mux.HandleFunc("GET /api/git-providers/github/launch", a.launchGitHubApp)
	mux.HandleFunc("GET /api/git-providers/github/created", a.gitHubAppCreated)
	mux.HandleFunc("GET /api/git-providers/github/installed", a.gitHubAppInstalled)
	mux.HandleFunc("GET /api/git-providers/oauth/callback", a.gitProviderOAuthCallback)
	mux.HandleFunc("POST /api/hooks/git-providers/{id}", a.gitProviderHook)
	mux.HandleFunc("GET /api/registries", a.listRegistries)
	mux.HandleFunc("POST /api/registries", a.createRegistry)
	mux.HandleFunc("PUT /api/registries/{id}", a.updateRegistry)
	mux.HandleFunc("DELETE /api/registries/{id}", a.deleteRegistry)
	mux.HandleFunc("POST /api/registries/{id}/test", a.testRegistry)
	mux.HandleFunc("GET /api/notifications", a.notifications)
	mux.HandleFunc("POST /api/notifications/channels", a.createChannel)
	mux.HandleFunc("PUT /api/notifications/channels/{id}", a.updateChannel)
	mux.HandleFunc("DELETE /api/notifications/channels/{id}", a.deleteChannel)
	mux.HandleFunc("POST /api/notifications/channels/{id}/test", a.testChannel)
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

func (a *API) applyUpdate(w http.ResponseWriter, r *http.Request) {
	info, err := a.core.ApplyUpdate(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, info)
}

func (a *API) checkUpdate(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.core.CheckUpdate(r.Context()))
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
		writeError(w, http.StatusUnauthorized, reason(err, core.ErrUnauthorized))
	case errors.Is(err, core.ErrForbidden):
		writeError(w, http.StatusForbidden, reason(err, core.ErrForbidden))
	case errors.Is(err, core.ErrInvalid):
		writeError(w, http.StatusBadRequest, reason(err, core.ErrInvalid))
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

// reason drops the sentinel's own text ("invalid input: ") so the UI shows
// only the explanation; a bare sentinel stays as is.
func reason(err, sentinel error) string {
	msg, prefix := err.Error(), sentinel.Error()+": "
	if i := strings.Index(msg, prefix); i >= 0 {
		return msg[i+len(prefix):]
	}
	return msg
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
