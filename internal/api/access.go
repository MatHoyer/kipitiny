package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/kipit-labs/kipitiny/internal/usecase/identity"
)

// access is what a route needs from a user or token: a permission on the
// project its {id} belongs to, a server feature, or being an admin. Routes
// for anyone signed in show only what core lets the caller see.
type access struct {
	perm    identity.Perm
	kind    identity.IDKind
	feature identity.Feature
	anyone  bool
}

var (
	anyone = access{anyone: true}
	admin  = access{}
)

func onProject(p identity.Perm) access    { return access{perm: p, kind: identity.IDProject} }
func onService(p identity.Perm) access    { return access{perm: p, kind: identity.IDService} }
func onBackup(p identity.Perm) access     { return access{perm: p, kind: identity.IDBackup} }
func onDeployment(p identity.Perm) access { return access{perm: p, kind: identity.IDDeployment} }
func onSchedule(p identity.Perm) access   { return access{perm: p, kind: identity.IDSchedule} }
func onJob(p identity.Perm) access        { return access{perm: p, kind: identity.IDJob} }
func feature(f identity.Feature) access   { return access{feature: f} }

// routeAccess lists every authenticated route (TestRouteAccess checks it);
// one missing would be admin only.
// Core checks what depends on the request body (env and domains in a
// service change, write mode in the data console, template installs).
var routeAccess = map[string]access{
	"POST /api/auth/logout": anyone,
	"GET /api/account":      anyone,
	// Tokens act for their owner: members manage their own.
	"GET /api/tokens":                   anyone,
	"POST /api/tokens":                  anyone,
	"PUT /api/tokens/{id}":              anyone,
	"POST /api/tokens/{id}/roll":        anyone,
	"DELETE /api/tokens/{id}":           anyone,
	"PUT /api/account/password":         anyone,
	"PUT /api/account/preferences":      anyone,
	"POST /api/account/totp":            anyone,
	"POST /api/account/totp/enable":     anyone,
	"POST /api/account/totp/disable":    anyone,
	"POST /api/account/recovery-codes":  anyone,
	"POST /api/account/passkeys/begin":  anyone,
	"POST /api/account/passkeys":        anyone,
	"DELETE /api/account/passkeys/{id}": anyone,
	// Push notifications go to admins; anyone removes their own browsers.
	"GET /api/account/push":              admin,
	"POST /api/account/push":             admin,
	"DELETE /api/account/push/{id}":      anyone,
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
	"POST /api/projects":                 feature(identity.FeatureCreateProjects),
	"GET /api/projects":                  anyone,
	// Core lists the tags of the visible projects and checks who may color one.
	"GET /api/tags":                       anyone,
	"PUT /api/tags/{name}":                anyone,
	"GET /api/projects/{id}":              onProject(identity.PermView),
	"DELETE /api/projects/{id}":           onProject(identity.PermDelete),
	"PUT /api/projects/{id}/env":          onProject(identity.PermEnv),
	"PUT /api/projects/{id}/name":         onProject(identity.PermSettings),
	"PUT /api/projects/{id}/sleep":        onProject(identity.PermSettings),
	"PUT /api/projects/{id}/tags":         onProject(identity.PermSettings),
	"PUT /api/projects/{id}/maintenance":  onProject(identity.PermDeploy),
	"GET /api/projects/{id}/compose":      onProject(identity.PermView),
	"POST /api/projects/{id}/compose":     onProject(identity.PermSettings),
	"POST /api/projects/{id}/export":      onProject(identity.PermDownload),
	"GET /api/projects/{id}/git":          onProject(identity.PermGit),
	"PUT /api/projects/{id}/git":          onProject(identity.PermGit),
	"DELETE /api/projects/{id}/git":       onProject(identity.PermGit),
	"POST /api/projects/{id}/git/preview": onProject(identity.PermGit),
	"POST /api/projects/{id}/git/sync":    onProject(identity.PermGit),
	"GET /api/projects/{id}/topology":     onProject(identity.PermView),
	// Previews of a project come with its deploy permission: CI opens and
	// closes them. Their settings hold the secrets they get instead of
	// production's.
	"GET /api/projects/{id}/previews":           onProject(identity.PermView),
	"PUT /api/projects/{id}/previews/{name}":    onProject(identity.PermDeploy),
	"DELETE /api/projects/{id}/previews/{name}": onProject(identity.PermDeploy),
	"GET /api/projects/{id}/preview-settings":   onProject(identity.PermView),
	"PUT /api/projects/{id}/preview-settings":   onProject(identity.PermEnv),
	// What previews are seeded with: anonymize rules and copies.
	"GET /api/projects/{id}/preview-seed":                      onProject(identity.PermView),
	"PUT /api/projects/{id}/preview-seed/{service}":            onProject(identity.PermEnv),
	"POST /api/projects/{id}/preview-seed/{service}/anonymize": onProject(identity.PermEnv),
	"GET /api/projects/{id}/services":                          onProject(identity.PermView),
	"POST /api/projects/{id}/services":                         onProject(identity.PermSettings),
	"POST /api/projects/{id}/backups":                          onProject(identity.PermBackups),
	"GET /api/services/{id}":                                   onService(identity.PermView),
	"PATCH /api/services/{id}":                                 onService(identity.PermSettings),
	"DELETE /api/services/{id}":                                onService(identity.PermDelete),
	"PUT /api/services/{id}/name":                              onService(identity.PermSettings),
	"PUT /api/services/{id}/networks":                          onService(identity.PermSettings),
	"PUT /api/services/{id}/tags":                              onService(identity.PermSettings),
	"POST /api/services/{id}/deploy":                           onService(identity.PermDeploy),
	"POST /api/services/{id}/rollback":                         onService(identity.PermDeploy),
	"POST /api/services/{id}/{action}":                         onService(identity.PermDeploy),
	"GET /api/services/{id}/deployments":                       onService(identity.PermView),
	"GET /api/services/{id}/logs":                              onService(identity.PermView),
	"GET /api/services/{id}/stats":                             onService(identity.PermView),
	"GET /api/services/{id}/uptime":                            onService(identity.PermView),
	"GET /api/services/{id}/analytics":                         onService(identity.PermView),
	"PUT /api/services/{id}/uptime":                            onService(identity.PermSettings),
	"DELETE /api/services/{id}/uptime":                         onService(identity.PermSettings),
	"GET /api/services/{id}/terminal":                          onService(identity.PermTerminal),
	"GET /api/services/{id}/connection":                        onService(identity.PermEnv),
	"GET /api/services/{id}/backups":                           onService(identity.PermView),
	"POST /api/services/{id}/backups":                          onService(identity.PermBackups),
	"GET /api/services/{id}/restores":                          onService(identity.PermView),
	"GET /api/services/{id}/schedules":                         onService(identity.PermView),
	"POST /api/services/{id}/schedules":                        onService(identity.PermBackups),
	"PUT /api/schedules/{id}":                                  onSchedule(identity.PermBackups),
	"DELETE /api/schedules/{id}":                               onSchedule(identity.PermBackups),
	"GET /api/services/{id}/jobs":                              onService(identity.PermView),
	"POST /api/services/{id}/jobs":                             onService(identity.PermSettings),
	"PUT /api/jobs/{id}":                                       onJob(identity.PermSettings),
	"DELETE /api/jobs/{id}":                                    onJob(identity.PermSettings),
	"POST /api/jobs/{id}/run":                                  onJob(identity.PermDeploy),
	"GET /api/jobs/{id}/runs":                                  onJob(identity.PermView),
	"GET /api/jobs/{id}/runs/{run}/log":                        onJob(identity.PermView),
	"GET /api/backups/{id}":                                    onBackup(identity.PermView),
	"DELETE /api/backups/{id}":                                 onBackup(identity.PermBackups),
	"GET /api/backups/{id}/download":                           onBackup(identity.PermDownload),
	"POST /api/backups/{id}/restore":                           onBackup(identity.PermRestore),
	"POST /api/backups/{id}/verify":                            onBackup(identity.PermBackups),
	"GET /api/deployments/{id}":                                onDeployment(identity.PermView),
	"GET /api/deployments/{id}/log":                            onDeployment(identity.PermView),
	"GET /api/storage":                                         feature(identity.FeatureStorage),
	"GET /api/registries":                                      feature(identity.FeatureRegistries),
	"GET /api/git-providers":                                   feature(identity.FeatureGitProviders),
	"GET /api/git-providers/{id}/repos":                        feature(identity.FeatureGitProviders),
	"GET /api/git-providers/{id}/branches":                     feature(identity.FeatureGitProviders),
	"GET /api/secret-providers":                                feature(identity.FeatureSecretProviders),
	"GET /api/secret-connections":                              feature(identity.FeatureSecretProviders),
	"GET /api/secret-connections/{id}/vaults":                  feature(identity.FeatureSecretProviders),
	"GET /api/secret-connections/{id}/items":                   feature(identity.FeatureSecretProviders),
	"GET /api/servers":                                         feature(identity.FeatureServers),
	"GET /api/ssh-key":                                         feature(identity.FeatureServers),
	"GET /api/networks":                                        feature(identity.FeatureNetworks),
	"GET /api/notifications":                                   feature(identity.FeatureNotifications),
	"GET /api/domains":                                         feature(identity.FeatureDomains),
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
	"POST /api/storage/{id}/scan":                admin,
	"POST /api/manager/backups":                  admin,
	"GET /api/manager/schedules":                 admin,
	"POST /api/manager/schedules":                admin,
	"POST /api/servers":                          admin,
	"PUT /api/servers/{id}":                      admin,
	"DELETE /api/servers/{id}":                   admin,
	"GET /api/servers/{id}/terminal":             admin,
	"GET /api/servers/{id}/stats":                feature(identity.FeatureServers),
	"GET /api/servers/{id}/security":             admin,
	"POST /api/servers/{id}/security/check":      admin,
	"POST /api/servers/{id}/security/updates":    admin,
	"POST /api/servers/{id}/reboot":              admin,
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
	"GET /api/analytics":                         admin,
	"PUT /api/analytics":                         admin,
	"DELETE /api/analytics/data":                 admin,
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
	"PUT /api/notifications/thresholds":          admin,
	"GET /api/users":                             admin,
	"PUT /api/users/{id}":                        admin,
	"DELETE /api/users/{id}":                     admin,
	"GET /api/invites":                           admin,
	"POST /api/invites":                          admin,
	"DELETE /api/invites/{id}":                   admin,
	"GET /api/audit":                             admin,

	"GET /api/services/{id}/analytics/visits":         onService(identity.PermView),
	"GET /api/services/{id}/errors":                   onService(identity.PermView),
	"GET /api/services/{id}/errors/series":            onService(identity.PermView),
	"GET /api/services/{id}/errors/deploy":            onService(identity.PermView),
	"GET /api/services/{id}/errors/{issue}":           onService(identity.PermView),
	"GET /api/services/{id}/errors/{issue}/event":     onService(identity.PermView),
	"PUT /api/services/{id}/errors/{issue}/status":    onService(identity.PermDeploy),
	"GET /api/services/{id}/analytics/export":         onService(identity.PermView),
	"GET /api/services/{id}/analytics/funnel":         onService(identity.PermView),
	"POST /api/services/{id}/analytics/share":         onService(identity.PermSettings),
	"DELETE /api/services/{id}/analytics/share":       onService(identity.PermSettings),
	"GET /api/services/{id}/analytics/visits/{visit}": onService(identity.PermView),
	"GET /api/analytics/trackers":                     admin,
	"POST /api/analytics/trackers":                    admin,
	"PUT /api/analytics/trackers/{id}":                admin,
	"DELETE /api/analytics/trackers/{id}":             admin,
	"GET /api/analytics/trackers/{id}/stats":          admin,
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
	actor, _ := identity.ActorFrom(ctx)
	if actor.Admin() {
		return nil
	}
	if dataRoute(pattern) {
		// Core checks data and files permissions, shared with MCP; the
		// service must still be visible.
		projectID, err := a.core.ProjectOf(ctx, identity.IDService, r.PathValue("id"))
		if err != nil {
			return err
		}
		return identity.Authorize(ctx, projectID, identity.PermView)
	}
	acc, ok := routeAccess[pattern]
	switch {
	case !ok:
		return identity.RequireAdmin(ctx)
	case acc.anyone:
		return nil
	case acc.feature != "":
		return identity.RequireFeature(ctx, acc.feature)
	case acc.perm != "":
		projectID, err := a.core.ProjectOf(ctx, acc.kind, r.PathValue("id"))
		if err != nil {
			return err
		}
		if projectID == "" { // the manager's own backups and schedules
			return identity.RequireAdmin(ctx)
		}
		return identity.Authorize(ctx, projectID, acc.perm)
	}
	return identity.RequireAdmin(ctx)
}
