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
	// Each route checks what the caller may do once the mux has set its
	// path values (protect runs before).
	handle := func(pattern string, h http.HandlerFunc) { mux.Handle(pattern, a.authorized(pattern, h)) }
	handle("GET /api/health", a.health)
	handle("GET /api/auth/state", a.authState)
	handle("POST /api/auth/setup", a.setup)
	handle("POST /api/auth/login", a.login)
	handle("POST /api/auth/login/mfa", a.loginMFA)
	handle("POST /api/auth/passkey/begin", a.beginPasskeyLogin)
	handle("POST /api/auth/passkey/finish", a.finishPasskeyLogin)
	handle("POST /api/auth/logout", a.logout)
	handle("POST /api/auth/sso/{id}/begin", a.beginSSOLogin)
	handle("POST /api/auth/sso/{id}/invite", a.beginSSOInvite)
	handle("GET /api/auth/sso/callback", a.ssoCallback)
	handle("GET /api/account", a.account)
	handle("POST /api/account/sso/{id}/connect", a.beginSSOConnect)
	handle("DELETE /api/account/sso/{id}", a.unlinkSSO)
	handle("POST /api/account/sso/{id}/confirm", a.beginSSOConfirm)
	handle("PUT /api/account/password", a.changePassword)
	handle("POST /api/account/totp", a.beginTOTP)
	handle("POST /api/account/totp/enable", a.enableTOTP)
	handle("POST /api/account/totp/disable", a.disableTOTP)
	handle("POST /api/account/recovery-codes", a.regenerateRecoveryCodes)
	handle("POST /api/account/passkeys/begin", a.beginPasskeyRegistration)
	handle("POST /api/account/passkeys", a.finishPasskeyRegistration)
	handle("DELETE /api/account/passkeys/{id}", a.deletePasskey)
	handle("GET /api/status", a.status)
	handle("POST /api/update", a.applyUpdate)
	handle("POST /api/update/check", a.checkUpdate)
	handle("GET /api/update/preflight", a.updatePreflight)
	handle("GET /api/projects", a.listProjects)
	handle("GET /api/tags", a.listTags)
	handle("PUT /api/tags/{name}", a.setTagColor)
	handle("POST /api/projects", a.createProject)
	handle("GET /api/projects/{id}", a.getProject)
	handle("DELETE /api/projects/{id}", a.deleteProject)
	handle("PUT /api/projects/{id}/env", a.setProjectEnv)
	handle("PUT /api/projects/{id}/name", a.renameProject)
	handle("PUT /api/projects/{id}/sleep", a.setProjectSleep)
	handle("PUT /api/projects/{id}/tags", a.setProjectTags)
	handle("PUT /api/projects/{id}/maintenance", a.setMaintenance)
	handle("GET /api/projects/{id}/compose", a.exportCompose)
	handle("POST /api/projects/{id}/compose", a.applyCompose)
	handle("POST /api/projects/{id}/export", a.exportBundle)
	handle("GET /api/projects/{id}/git", a.projectGit)
	handle("GET /api/templates", a.listTemplates)
	handle("GET /api/templates/logos", a.templateLogos)
	handle("POST /api/templates/{id}/install", a.installTemplate)
	handle("PUT /api/projects/{id}/git", a.linkProjectGit)
	handle("DELETE /api/projects/{id}/git", a.unlinkProjectGit)
	handle("POST /api/projects/{id}/git/preview", a.previewProjectGit)
	handle("POST /api/projects/{id}/git/sync", a.syncProjectGit)
	handle("POST /api/hooks/projects/{id}", a.gitHook)
	handle("GET /api/projects/{id}/topology", a.topology)
	handle("GET /api/projects/{id}/previews", a.listPreviews)
	handle("PUT /api/projects/{id}/previews/{name}", a.deployPreview)
	handle("DELETE /api/projects/{id}/previews/{name}", a.closePreview)
	handle("GET /api/projects/{id}/preview-settings", a.previewSettings)
	handle("PUT /api/projects/{id}/preview-settings", a.setPreviewSettings)
	handle("GET /api/topology", a.topology)
	handle("GET /api/projects/{id}/services", a.listServices)
	handle("POST /api/projects/{id}/services", a.createService)
	handle("GET /api/services/{id}", a.getService)
	handle("PATCH /api/services/{id}", a.updateService)
	handle("DELETE /api/services/{id}", a.deleteService)
	handle("PUT /api/services/{id}/name", a.renameService)
	handle("PUT /api/services/{id}/networks", a.setServiceNetworks)
	handle("PUT /api/services/{id}/tags", a.setServiceTags)
	handle("POST /api/services/{id}/deploy", a.deployService)
	handle("POST /api/services/{id}/rollback", a.rollbackService)
	handle("POST /api/services/{id}/{action}", a.serviceAction)
	handle("GET /api/services/{id}/deployments", a.listDeployments)
	handle("GET /api/services/{id}/logs", a.streamLogs)
	handle("GET /api/services/{id}/stats", a.serviceStats)
	handle("GET /api/services/{id}/uptime", a.uptime)
	handle("PUT /api/services/{id}/uptime", a.setUptime)
	handle("DELETE /api/services/{id}/uptime", a.deleteUptime)
	handle("GET /api/stats", a.usage)
	handle("GET /api/services/{id}/terminal", a.serviceTerminal)
	handle("GET /api/services/{id}/connection", a.connection)
	handle("GET /api/services/{id}/data/databases", a.pgDatabases)
	handle("POST /api/services/{id}/data/databases", a.createPgDatabase)
	handle("GET /api/services/{id}/data/tables", a.pgTables)
	handle("GET /api/services/{id}/data/tables/{schema}/{table}", a.pgRows)
	handle("GET /api/services/{id}/data/tables/{schema}/{table}/export", a.pgExport)
	handle("POST /api/services/{id}/data/tables/{schema}/{table}/changes", a.rowChanges)
	handle("POST /api/services/{id}/data/tables", a.createTable)
	handle("POST /api/services/{id}/data/tables/{schema}/{table}/drop", a.dropTable)
	handle("POST /api/services/{id}/data/collections", a.createCollection)
	handle("POST /api/services/{id}/data/collections/{collection}/drop", a.dropCollection)
	handle("GET /api/services/{id}/data/collections/{collection}/documents", a.mongoDocuments)
	handle("POST /api/services/{id}/data/collections/{collection}/changes", a.docChanges)
	handle("GET /api/services/{id}/data/keys", a.redisScan)
	handle("GET /api/services/{id}/data/key", a.redisGet)
	handle("POST /api/services/{id}/data/key/changes", a.keyChanges)
	handle("POST /api/services/{id}/data/console", a.dataConsole)
	handle("GET /api/services/{id}/files", a.listVolumeFiles)
	handle("GET /api/services/{id}/files/content", a.readVolumeFile)
	handle("PUT /api/services/{id}/files/content", a.writeVolumeFile)
	handle("GET /api/services/{id}/files/download", a.downloadVolumeFiles)
	handle("POST /api/services/{id}/files/mkdir", a.makeVolumeDir)
	handle("POST /api/services/{id}/files/move", a.moveVolumePath)
	handle("POST /api/services/{id}/files/copy", a.copyVolumePath)
	handle("POST /api/services/{id}/files/delete", a.deleteVolumePaths)
	handle("GET /api/services/{id}/backups", a.listServiceBackups)
	handle("POST /api/services/{id}/backups", a.createBackup)
	handle("GET /api/services/{id}/restores", a.listRestores)
	handle("GET /api/services/{id}/schedules", a.listSchedules)
	handle("POST /api/services/{id}/schedules", a.createSchedule)
	handle("PUT /api/schedules/{id}", a.updateSchedule)
	handle("DELETE /api/schedules/{id}", a.deleteSchedule)
	handle("POST /api/projects/{id}/backups", a.backupProject)
	handle("GET /api/backups", a.listBackups)
	handle("GET /api/backups/{id}", a.getBackup)
	handle("DELETE /api/backups/{id}", a.deleteBackup)
	handle("GET /api/backups/{id}/download", a.downloadBackup)
	handle("POST /api/backups/{id}/restore", a.restoreBackup)
	handle("POST /api/backups/{id}/verify", a.verifyBackup)
	handle("GET /api/storage", a.listStorage)
	handle("POST /api/storage", a.createStorage)
	handle("PUT /api/storage/{id}", a.updateStorage)
	handle("DELETE /api/storage/{id}", a.deleteStorage)
	handle("GET /api/storage/{id}/key", a.storageKey)
	handle("POST /api/storage/{id}/test", a.testStorage)
	handle("POST /api/storage/{id}/encrypt", a.encryptStorage)
	handle("POST /api/manager/backups", a.backupManager)
	handle("GET /api/manager/schedules", a.listManagerSchedules)
	handle("POST /api/manager/schedules", a.createManagerSchedule)
	handle("GET /api/deployments/{id}", a.getDeployment)
	handle("GET /api/deployments/{id}/log", a.deploymentLog)
	handle("GET /api/servers", a.listServers)
	handle("POST /api/servers", a.createServer)
	handle("PUT /api/servers/{id}", a.updateServer)
	handle("DELETE /api/servers/{id}", a.deleteServer)
	handle("GET /api/servers/{id}/terminal", a.serverTerminal)
	handle("GET /api/ssh-key", a.sshKey)
	handle("GET /api/domains", a.listDomains)
	handle("POST /api/domains", a.createDomain)
	handle("PATCH /api/domains/{id}", a.updateDomain)
	handle("DELETE /api/domains/{id}", a.deleteDomain)
	handle("GET /api/cloudflare", a.cloudflareStatus)
	handle("PUT /api/cloudflare", a.connectCloudflare)
	handle("DELETE /api/cloudflare", a.disconnectCloudflare)
	handle("POST /api/cloudflare/test", a.testCloudflare)
	handle("POST /api/cloudflare/sync", a.syncCloudflare)
	handle("GET /api/email", a.email)
	handle("PUT /api/email/invites", a.setEmailInvites)
	handle("POST /api/email/providers", a.createEmailProvider)
	handle("PUT /api/email/providers/{id}", a.updateEmailProvider)
	handle("DELETE /api/email/providers/{id}", a.deleteEmailProvider)
	handle("POST /api/email/providers/{id}/test", a.testEmailProvider)
	handle("GET /api/sso-providers", a.listSSOProviders)
	handle("POST /api/sso-providers", a.createSSOProvider)
	handle("PUT /api/sso-providers/{id}", a.updateSSOProvider)
	handle("DELETE /api/sso-providers/{id}", a.deleteSSOProvider)
	handle("GET /api/sso-required", a.ssoRequired)
	handle("PUT /api/sso-required", a.setSSORequired)
	handle("PUT /api/servers/{id}/network", a.setServerNetwork)
	handle("GET /api/networks", a.listNetworks)
	handle("GET /api/canvas", a.canvasLayout)
	handle("PUT /api/canvas", a.saveCanvasLayout)
	handle("DELETE /api/canvas", a.resetCanvasLayout)
	handle("POST /api/networks", a.createNetwork)
	handle("DELETE /api/networks/{id}", a.deleteNetwork)
	handle("GET /api/secret-providers", a.secretProviders)
	handle("GET /api/secret-connections", a.secretConnections)
	handle("POST /api/secret-connections", a.createSecretConnection)
	handle("PUT /api/secret-connections/{id}", a.updateSecretConnection)
	handle("DELETE /api/secret-connections/{id}", a.deleteSecretConnection)
	handle("GET /api/secret-connections/{id}/vaults", a.secretVaults)
	handle("POST /api/secret-connections/{id}/test", a.testSecretConnection)
	handle("GET /api/secret-connections/{id}/items", a.secretItems)
	handle("GET /api/branding/logo", a.brandingLogo)
	handle("PUT /api/branding", a.setBranding)
	handle("DELETE /api/branding", a.resetBranding)
	handle("GET /api/cleanup", a.getCleanup)
	handle("PUT /api/cleanup", a.setCleanup)
	handle("POST /api/cleanup/run", a.runCleanup)
	handle("GET /api/git-providers", a.listGitProviders)
	handle("POST /api/git-providers", a.createGitProvider)
	handle("PUT /api/git-providers/{id}", a.updateGitProvider)
	handle("DELETE /api/git-providers/{id}", a.deleteGitProvider)
	handle("POST /api/git-providers/{id}/test", a.testGitProvider)
	handle("POST /api/git-providers/{id}/authorize", a.authorizeGitProvider)
	handle("GET /api/git-providers/{id}/repos", a.gitProviderRepos)
	handle("GET /api/git-providers/{id}/branches", a.gitProviderBranches)
	handle("POST /api/git-providers/github", a.startGitHubApp)
	handle("GET /api/git-providers/github/launch", a.launchGitHubApp)
	handle("GET /api/git-providers/github/created", a.gitHubAppCreated)
	handle("GET /api/git-providers/github/installed", a.gitHubAppInstalled)
	handle("GET /api/git-providers/oauth/callback", a.gitProviderOAuthCallback)
	handle("POST /api/hooks/git-providers/{id}", a.gitProviderHook)
	handle("GET /api/registries", a.listRegistries)
	handle("POST /api/registries", a.createRegistry)
	handle("PUT /api/registries/{id}", a.updateRegistry)
	handle("DELETE /api/registries/{id}", a.deleteRegistry)
	handle("POST /api/registries/{id}/test", a.testRegistry)
	handle("GET /api/notifications", a.notifications)
	handle("POST /api/notifications/channels", a.createChannel)
	handle("PUT /api/notifications/channels/{id}", a.updateChannel)
	handle("DELETE /api/notifications/channels/{id}", a.deleteChannel)
	handle("POST /api/notifications/channels/{id}/test", a.testChannel)
	handle("GET /api/tokens", a.listTokens)
	handle("POST /api/tokens", a.createToken)
	handle("PUT /api/tokens/{id}", a.updateToken)
	handle("POST /api/tokens/{id}/roll", a.rollToken)
	handle("DELETE /api/tokens/{id}", a.deleteToken)
	handle("GET /api/audit", a.listAudit)
	handle("GET /api/users", a.listUsers)
	handle("PUT /api/users/{id}", a.setUserAccess)
	handle("DELETE /api/users/{id}", a.deleteUser)
	handle("GET /api/invites", a.listInvites)
	handle("POST /api/invites", a.createInvite)
	handle("DELETE /api/invites/{id}", a.deleteInvite)
	handle("POST /api/auth/invite", a.invite)
	handle("POST /api/auth/invite/accept", a.acceptInvite)
	handle("/api/", func(w http.ResponseWriter, r *http.Request) {
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

func (a *API) updatePreflight(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.core.UpdatePreflight(r.Context()))
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
	writeJSON(w, http.StatusOK, map[string]any{"tags": tags, "colors": core.TagColors})
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
	var in core.MaintenanceInput
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

func (a *API) fail(w http.ResponseWriter, err error) {
	code, msg := a.failure(err)
	writeError(w, code, msg)
}

// failure maps err to the status and message the API answers with.
func (a *API) failure(err error) (int, string) {
	switch {
	case errors.Is(err, core.ErrUnauthorized):
		return http.StatusUnauthorized, reason(err, core.ErrUnauthorized)
	case errors.Is(err, core.ErrForbidden):
		return http.StatusForbidden, reason(err, core.ErrForbidden)
	case errors.Is(err, core.ErrInvalid):
		return http.StatusBadRequest, reason(err, core.ErrInvalid)
	case errors.Is(err, store.ErrNotFound):
		return http.StatusNotFound, "not found"
	case errors.Is(err, store.ErrConflict):
		msg := "already exists (name or domain taken)"
		if strings.Contains(err.Error(), store.ErrConflict.Error()+": ") {
			msg = reason(err, store.ErrConflict) // core's own explanation
		}
		return http.StatusConflict, msg
	case errors.Is(err, core.ErrBusy):
		return http.StatusConflict, "another operation is in progress for this service"
	default:
		a.log.Error("request failed", "err", err)
		return http.StatusInternalServerError, "internal error"
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
