package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/MatHoyer/kipitiny/internal/core"
)

// access is what a route needs from a user or token: a permission on the
// project its {id} belongs to, a server feature, or being an admin. Routes
// for anyone signed in show only what core lets the caller see.
type access struct {
	perm    core.Perm
	kind    core.IDKind
	feature core.Feature
	anyone  bool
}

var (
	anyone = access{anyone: true}
	admin  = access{}
)

func onProject(p core.Perm) access    { return access{perm: p, kind: core.IDProject} }
func onService(p core.Perm) access    { return access{perm: p, kind: core.IDService} }
func onBackup(p core.Perm) access     { return access{perm: p, kind: core.IDBackup} }
func onDeployment(p core.Perm) access { return access{perm: p, kind: core.IDDeployment} }
func onSchedule(p core.Perm) access   { return access{perm: p, kind: core.IDSchedule} }
func feature(f core.Feature) access   { return access{feature: f} }

// routeAccess lists every authenticated route (TestRouteAccess checks it);
// one missing would be admin only.
// Core checks what depends on the request body (env and domains in a
// service change, write mode in the data console, template installs).
var routeAccess = map[string]access{
	"POST /api/auth/logout": anyone,
	"GET /api/account":      anyone,
	// Tokens act for their owner: members manage their own.
	"GET /api/tokens":                    anyone,
	"POST /api/tokens":                   anyone,
	"PUT /api/tokens/{id}":               anyone,
	"POST /api/tokens/{id}/roll":         anyone,
	"DELETE /api/tokens/{id}":            anyone,
	"PUT /api/account/password":          anyone,
	"POST /api/account/totp":             anyone,
	"POST /api/account/totp/enable":      anyone,
	"POST /api/account/totp/disable":     anyone,
	"POST /api/account/recovery-codes":   anyone,
	"POST /api/account/passkeys/begin":   anyone,
	"POST /api/account/passkeys":         anyone,
	"DELETE /api/account/passkeys/{id}":  anyone,
	"POST /api/account/sso/{id}/connect": anyone,
	"DELETE /api/account/sso/{id}":       anyone,
	"POST /api/account/sso/{id}/confirm": anyone,
	"GET /api/status":                    anyone,
	"GET /api/stats":                     anyone,
	"GET /api/topology":                  anyone,
	"GET /api/canvas":                    anyone,
	"GET /api/templates":                 anyone,
	"GET /api/templates/logos":           anyone,
	"POST /api/templates/{id}/install":   anyone,
	"GET /api/backups":                   anyone,
	"POST /api/projects":                 feature(core.FeatureCreateProjects),
	"GET /api/projects":                  anyone,
	// Core lists the tags of the visible projects and checks who may color one.
	"GET /api/tags":                       anyone,
	"PUT /api/tags/{name}":                anyone,
	"GET /api/projects/{id}":              onProject(core.PermView),
	"DELETE /api/projects/{id}":           onProject(core.PermDelete),
	"PUT /api/projects/{id}/env":          onProject(core.PermEnv),
	"PUT /api/projects/{id}/name":         onProject(core.PermSettings),
	"PUT /api/projects/{id}/sleep":        onProject(core.PermSettings),
	"PUT /api/projects/{id}/tags":         onProject(core.PermSettings),
	"PUT /api/projects/{id}/maintenance":  onProject(core.PermDeploy),
	"GET /api/projects/{id}/compose":      onProject(core.PermView),
	"POST /api/projects/{id}/compose":     onProject(core.PermSettings),
	"POST /api/projects/{id}/export":      onProject(core.PermDownload),
	"GET /api/projects/{id}/git":          onProject(core.PermGit),
	"PUT /api/projects/{id}/git":          onProject(core.PermGit),
	"DELETE /api/projects/{id}/git":       onProject(core.PermGit),
	"POST /api/projects/{id}/git/preview": onProject(core.PermGit),
	"POST /api/projects/{id}/git/sync":    onProject(core.PermGit),
	"GET /api/projects/{id}/topology":     onProject(core.PermView),
	// Previews of a project come with its deploy permission: CI opens and
	// closes them. Their settings hold the secrets they get instead of
	// production's.
	"GET /api/projects/{id}/previews":           onProject(core.PermView),
	"PUT /api/projects/{id}/previews/{name}":    onProject(core.PermDeploy),
	"DELETE /api/projects/{id}/previews/{name}": onProject(core.PermDeploy),
	"GET /api/projects/{id}/preview-settings":   onProject(core.PermView),
	"PUT /api/projects/{id}/preview-settings":   onProject(core.PermEnv),
	"GET /api/projects/{id}/services":           onProject(core.PermView),
	"POST /api/projects/{id}/services":          onProject(core.PermSettings),
	"POST /api/projects/{id}/backups":           onProject(core.PermBackups),
	"GET /api/services/{id}":                    onService(core.PermView),
	"PATCH /api/services/{id}":                  onService(core.PermSettings),
	"DELETE /api/services/{id}":                 onService(core.PermDelete),
	"PUT /api/services/{id}/name":               onService(core.PermSettings),
	"PUT /api/services/{id}/networks":           onService(core.PermSettings),
	"PUT /api/services/{id}/tags":               onService(core.PermSettings),
	"POST /api/services/{id}/deploy":            onService(core.PermDeploy),
	"POST /api/services/{id}/rollback":          onService(core.PermDeploy),
	"POST /api/services/{id}/{action}":          onService(core.PermDeploy),
	"GET /api/services/{id}/deployments":        onService(core.PermView),
	"GET /api/services/{id}/logs":               onService(core.PermView),
	"GET /api/services/{id}/stats":              onService(core.PermView),
	"GET /api/services/{id}/uptime":             onService(core.PermView),
	"PUT /api/services/{id}/uptime":             onService(core.PermSettings),
	"DELETE /api/services/{id}/uptime":          onService(core.PermSettings),
	"GET /api/services/{id}/terminal":           onService(core.PermTerminal),
	"GET /api/services/{id}/connection":         onService(core.PermEnv),
	"GET /api/services/{id}/backups":            onService(core.PermView),
	"POST /api/services/{id}/backups":           onService(core.PermBackups),
	"GET /api/services/{id}/restores":           onService(core.PermView),
	"GET /api/services/{id}/schedules":          onService(core.PermView),
	"POST /api/services/{id}/schedules":         onService(core.PermBackups),
	"PUT /api/schedules/{id}":                   onSchedule(core.PermBackups),
	"DELETE /api/schedules/{id}":                onSchedule(core.PermBackups),
	"GET /api/backups/{id}":                     onBackup(core.PermView),
	"DELETE /api/backups/{id}":                  onBackup(core.PermBackups),
	"GET /api/backups/{id}/download":            onBackup(core.PermDownload),
	"POST /api/backups/{id}/restore":            onBackup(core.PermRestore),
	"POST /api/backups/{id}/verify":             onBackup(core.PermBackups),
	"GET /api/deployments/{id}":                 onDeployment(core.PermView),
	"GET /api/deployments/{id}/log":             onDeployment(core.PermView),
	"GET /api/storage":                          feature(core.FeatureStorage),
	"GET /api/registries":                       feature(core.FeatureRegistries),
	"GET /api/git-providers":                    feature(core.FeatureGitProviders),
	"GET /api/git-providers/{id}/repos":         feature(core.FeatureGitProviders),
	"GET /api/git-providers/{id}/branches":      feature(core.FeatureGitProviders),
	"GET /api/secret-providers":                 feature(core.FeatureSecretProviders),
	"GET /api/secret-connections":               feature(core.FeatureSecretProviders),
	"GET /api/secret-connections/{id}/vaults":   feature(core.FeatureSecretProviders),
	"GET /api/secret-connections/{id}/items":    feature(core.FeatureSecretProviders),
	"GET /api/servers":                          feature(core.FeatureServers),
	"GET /api/ssh-key":                          feature(core.FeatureServers),
	"GET /api/networks":                         feature(core.FeatureNetworks),
	"GET /api/notifications":                    feature(core.FeatureNotifications),
	"GET /api/domains":                          feature(core.FeatureDomains),
	// Unknown routes answer 404 to anyone.
	"/api/": anyone,
	// The server itself and its settings.
	"POST /api/update":                           admin,
	"POST /api/update/check":                     admin,
	"GET /api/update/preflight":                  admin,
	"POST /api/storage":                          admin,
	"PUT /api/storage/{id}":                      admin,
	"DELETE /api/storage/{id}":                   admin,
	"GET /api/storage/{id}/key":                  admin,
	"POST /api/storage/{id}/test":                admin,
	"POST /api/storage/{id}/encrypt":             admin,
	"POST /api/manager/backups":                  admin,
	"GET /api/manager/schedules":                 admin,
	"POST /api/manager/schedules":                admin,
	"POST /api/servers":                          admin,
	"PUT /api/servers/{id}":                      admin,
	"DELETE /api/servers/{id}":                   admin,
	"GET /api/servers/{id}/terminal":             admin,
	"GET /api/servers/{id}/stats":                feature(core.FeatureServers),
	"GET /api/servers/{id}/security":             admin,
	"POST /api/servers/{id}/security/check":      admin,
	"POST /api/domains":                          admin,
	"PATCH /api/domains/{id}":                    admin,
	"DELETE /api/domains/{id}":                   admin,
	"GET /api/cloudflare":                        admin,
	"PUT /api/cloudflare":                        admin,
	"DELETE /api/cloudflare":                     admin,
	"POST /api/cloudflare/test":                  admin,
	"POST /api/cloudflare/sync":                  admin,
	"GET /api/email":                             admin,
	"PUT /api/email/invites":                     admin,
	"POST /api/email/providers":                  admin,
	"PUT /api/email/providers/{id}":              admin,
	"DELETE /api/email/providers/{id}":           admin,
	"POST /api/email/providers/{id}/test":        admin,
	"GET /api/sso-providers":                     admin,
	"POST /api/sso-providers":                    admin,
	"PUT /api/sso-providers/{id}":                admin,
	"DELETE /api/sso-providers/{id}":             admin,
	"GET /api/sso-required":                      admin,
	"PUT /api/sso-required":                      admin,
	"PUT /api/servers/{id}/network":              admin,
	"PUT /api/canvas":                            admin,
	"DELETE /api/canvas":                         admin,
	"POST /api/networks":                         admin,
	"DELETE /api/networks/{id}":                  admin,
	"POST /api/secret-connections":               admin,
	"PUT /api/secret-connections/{id}":           admin,
	"DELETE /api/secret-connections/{id}":        admin,
	"POST /api/secret-connections/{id}/test":     admin,
	"PUT /api/branding":                          admin,
	"DELETE /api/branding":                       admin,
	"GET /api/cleanup":                           admin,
	"PUT /api/cleanup":                           admin,
	"POST /api/cleanup/run":                      admin,
	"POST /api/git-providers":                    admin,
	"PUT /api/git-providers/{id}":                admin,
	"DELETE /api/git-providers/{id}":             admin,
	"POST /api/git-providers/{id}/test":          admin,
	"POST /api/git-providers/{id}/authorize":     admin,
	"POST /api/git-providers/github":             admin,
	"GET /api/git-providers/github/launch":       admin,
	"GET /api/git-providers/github/created":      admin,
	"GET /api/git-providers/github/installed":    admin,
	"GET /api/git-providers/oauth/callback":      admin,
	"POST /api/registries":                       admin,
	"PUT /api/registries/{id}":                   admin,
	"DELETE /api/registries/{id}":                admin,
	"POST /api/registries/{id}/test":             admin,
	"POST /api/notifications/channels":           admin,
	"PUT /api/notifications/channels/{id}":       admin,
	"DELETE /api/notifications/channels/{id}":    admin,
	"POST /api/notifications/channels/{id}/test": admin,
	"GET /api/users":                             admin,
	"PUT /api/users/{id}":                        admin,
	"DELETE /api/users/{id}":                     admin,
	"GET /api/invites":                           admin,
	"POST /api/invites":                          admin,
	"DELETE /api/invites/{id}":                   admin,
	"GET /api/audit":                             admin,
}

// dataRoute is the data browser's or a volume files route: core checks
// their permissions, for MCP too.
func dataRoute(pattern string) bool {
	return strings.Contains(pattern, " /api/services/{id}/data/") || strings.Contains(pattern, " /api/services/{id}/files")
}

// authorized runs h once the context's actor passes the route's access.
// Refused mutations are still audited by protect, with their status.
func (a *API) authorized(pattern string, h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if publicRoutes[pattern] {
			h(w, r)
			return
		}
		if err := a.authorize(r.Context(), pattern, r); err != nil {
			a.fail(w, err)
			return
		}
		h(w, r)
	})
}

// authorize checks the route's access for the context's actor.
func (a *API) authorize(ctx context.Context, pattern string, r *http.Request) error {
	actor, _ := core.ActorFrom(ctx)
	if actor.Admin() {
		return nil
	}
	if dataRoute(pattern) {
		// Core checks data and files permissions, shared with MCP; the
		// service must still be visible.
		projectID, err := a.core.ProjectOf(ctx, core.IDService, r.PathValue("id"))
		if err != nil {
			return err
		}
		return core.Authorize(ctx, projectID, core.PermView)
	}
	acc, ok := routeAccess[pattern]
	switch {
	case !ok:
		return core.RequireAdmin(ctx)
	case acc.anyone:
		return nil
	case acc.feature != "":
		return core.RequireFeature(ctx, acc.feature)
	case acc.perm != "":
		projectID, err := a.core.ProjectOf(ctx, acc.kind, r.PathValue("id"))
		if err != nil {
			return err
		}
		if projectID == "" { // the manager's own backups and schedules
			return core.RequireAdmin(ctx)
		}
		return core.Authorize(ctx, projectID, acc.perm)
	}
	return core.RequireAdmin(ctx)
}
