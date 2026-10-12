package core

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"

	"github.com/MatHoyer/kipitiny/internal/store"
)

// Perm is something a member may do in a project they were granted.
type Perm string

const (
	// PermView reads the project: services, logs, stats, deployments, uptime.
	// Any other permission implies it.
	PermView      Perm = "view"
	PermDeploy    Perm = "deploy"   // deploy, rollback, start/stop/restart, maintenance
	PermSettings  Perm = "settings" // create and edit services, compose, networks, uptime checks
	PermEnv       Perm = "env"      // env and secrets, connection strings
	PermDomains   Perm = "domains"
	PermBackups   Perm = "backups" // back up now, verify, schedules
	PermRestore   Perm = "restore"
	PermDownload  Perm = "download" // backup downloads, project export with secrets
	PermData      Perm = "data"     // data browser and read-only console
	PermDataEdit  Perm = "data-write"
	PermFiles     Perm = "files"
	PermFilesEdit Perm = "files-write"
	PermTerminal  Perm = "terminal"
	PermGit       Perm = "git"
	PermDelete    Perm = "delete"
)

// Perms lists every project permission, in the order the UI shows them.
var Perms = []Perm{
	PermView, PermDeploy, PermSettings, PermEnv, PermDomains, PermBackups, PermRestore, PermDownload,
	PermData, PermDataEdit, PermFiles, PermFilesEdit, PermTerminal, PermGit, PermDelete,
}

// Feature is a server-level resource a member may see, read-only and
// without secrets, to use it in their projects.
type Feature string

const (
	FeatureStorage         Feature = "storage"
	FeatureRegistries      Feature = "registries"
	FeatureGitProviders    Feature = "git-providers"
	FeatureSecretProviders Feature = "secret-providers"
	FeatureServers         Feature = "servers"
	FeatureNetworks        Feature = "networks"
	FeatureNotifications   Feature = "notifications"
	FeatureDomains         Feature = "domains"
	// FeatureCreateProjects lets a member create projects, with every
	// permission on them.
	FeatureCreateProjects Feature = "create-projects"
)

var Features = []Feature{
	FeatureStorage, FeatureRegistries, FeatureGitProviders, FeatureSecretProviders, FeatureServers,
	FeatureNetworks, FeatureNotifications, FeatureDomains, FeatureCreateProjects,
}

// Admin reports whether the actor may do everything (still within a
// token's scope).
func (a Actor) Admin() bool { return a.Role == store.RoleAdmin }

// Can reports whether the actor holds p in the project.
func (a Actor) Can(projectID string, p Perm) bool {
	if a.Admin() {
		return true
	}
	perms := a.Grants[projectID]
	return len(perms) > 0 && (p == PermView || slices.Contains(perms, string(p)))
}

// Has reports whether the actor may see a server-level resource.
func (a Actor) Has(f Feature) bool {
	return a.Admin() || slices.Contains(a.Features, string(f))
}

// Authorize fails unless the context's actor holds p in the project. A
// project the actor can't see at all is reported as not found.
func Authorize(ctx context.Context, projectID string, p Perm) error {
	a, ok := ActorFrom(ctx)
	if !ok {
		return ErrUnauthorized
	}
	if !a.Can(projectID, PermView) {
		return fmt.Errorf("%w: project", store.ErrNotFound)
	}
	if !a.Can(projectID, p) {
		return fmt.Errorf("%w: needs the %s permission on this project", ErrForbidden, p)
	}
	return nil
}

// RequireFeature fails unless the context's actor may see f.
func RequireFeature(ctx context.Context, f Feature) error {
	a, ok := ActorFrom(ctx)
	if !ok {
		return ErrUnauthorized
	}
	if !a.Has(f) {
		return fmt.Errorf("%w: needs access to %s", ErrForbidden, f)
	}
	return nil
}

// RequireAdmin fails unless the context's actor is an admin.
func RequireAdmin(ctx context.Context) error {
	a, ok := ActorFrom(ctx)
	if !ok {
		return ErrUnauthorized
	}
	if !a.Admin() {
		return fmt.Errorf("%w: admins only", ErrForbidden)
	}
	return nil
}

// visible reports whether the context's actor may see the project. Work
// without an actor (loops, the CLI) sees everything.
func visible(ctx context.Context, projectID string) bool {
	a, ok := ActorFrom(ctx)
	return !ok || a.Can(projectID, PermView)
}

// isMember reports whether the context's actor is a member, who sees
// server-level resources without how they connect (hosts, accounts, URLs).
func isMember(ctx context.Context) bool {
	a, ok := ActorFrom(ctx)
	return ok && !a.Admin()
}

// visibleProjects is nil when every project is visible, else the IDs of
// the visible ones.
func visibleProjects(ctx context.Context) []string {
	a, ok := ActorFrom(ctx)
	if !ok || a.Admin() {
		return nil
	}
	ids := make([]string, 0, len(a.Grants))
	for id := range a.Grants {
		ids = append(ids, id)
	}
	return ids
}

// IDKind names what a route's {id} is, to find its project.
type IDKind string

const (
	IDProject    IDKind = "project"
	IDService    IDKind = "service"
	IDBackup     IDKind = "backup"
	IDDeployment IDKind = "deployment"
	IDSchedule   IDKind = "schedule"
)

// ProjectOf returns the project the resource belongs to. Manager backups
// and schedules belong to none (empty).
func (c *Core) ProjectOf(ctx context.Context, kind IDKind, id string) (string, error) {
	switch kind {
	case IDProject:
		p, err := c.store.GetProject(ctx, id)
		return p.ID, err
	case IDService:
		s, err := c.store.GetService(ctx, id)
		return s.ProjectID, err
	case IDBackup:
		b, err := c.store.GetBackup(ctx, id)
		return b.ProjectID, err
	case IDDeployment:
		d, err := c.store.GetDeployment(ctx, id)
		if err != nil {
			return "", err
		}
		return c.ProjectOf(ctx, IDService, d.ServiceID)
	case IDSchedule:
		s, err := c.store.GetBackupSchedule(ctx, id)
		if err != nil || s.ServiceID == "" {
			return "", err
		}
		return c.ProjectOf(ctx, IDService, s.ServiceID)
	}
	return "", errors.New("unknown resource kind " + string(kind))
}

// AuthorizeService resolves a service (ID or project/service) and checks p
// on its project.
func (c *Core) AuthorizeService(ctx context.Context, ref string, p Perm) (store.Service, error) {
	s, err := c.ResolveService(ctx, ref)
	if err != nil {
		return store.Service{}, err
	}
	if err := Authorize(ctx, s.ProjectID, p); err != nil {
		return store.Service{}, err
	}
	return s, nil
}

// UserActor is the actor of a signed-in user, with their grants.
func (c *Core) UserActor(ctx context.Context, u store.User) (Actor, error) {
	a := Actor{Kind: "user", Name: u.Username, Scope: store.ScopeAdmin, UserID: u.ID, Role: u.Role, Features: u.Features}
	return c.withGrants(ctx, a)
}

func (c *Core) withGrants(ctx context.Context, a Actor) (Actor, error) {
	if a.Admin() {
		return a, nil
	}
	gs, err := c.store.ListGrants(ctx, a.UserID)
	if err != nil {
		return Actor{}, err
	}
	a.Grants = make(map[string][]string, len(gs))
	for _, g := range gs {
		a.Grants[g.ProjectID] = g.Perms
	}
	return a, nil
}

// check is Authorize for work that may run without an actor (loops, the
// CLI, a git sync), which is allowed everything.
func check(ctx context.Context, projectID string, p Perm) error {
	if _, ok := ActorFrom(ctx); !ok {
		return nil
	}
	return Authorize(ctx, projectID, p)
}

// checkServiceChange checks what a change from old to svc needs beyond the
// settings permission: env and domains have their own, and host access
// (the host's network or Docker socket) is root on the host, so only
// admins give it.
func checkServiceChange(ctx context.Context, old, svc store.Service) error {
	a, ok := ActorFrom(ctx)
	if !ok || a.Admin() {
		return nil
	}
	if (svc.HostNetwork && !old.HostNetwork) || (svc.DockerSocket != "" && svc.DockerSocket != old.DockerSocket) {
		return fmt.Errorf("%w: only admins give an app the host's network or Docker socket", ErrForbidden)
	}
	if !maps.Equal(svc.Env, old.Env) || !slices.Equal(svc.Secrets, old.Secrets) {
		if err := Authorize(ctx, svc.ProjectID, PermEnv); err != nil {
			return err
		}
		if err := checkSecretRefs(ctx, old.Env, svc.Env); err != nil {
			return err
		}
	}
	if svc.Domain != old.Domain || !reflect.DeepEqual(svc.Middlewares, old.Middlewares) {
		if err := Authorize(ctx, svc.ProjectID, PermDomains); err != nil {
			return err
		}
	}
	return nil
}

// checkSecretRefs fails when a member references a password manager item
// that wasn't referenced before without the secret-providers feature: the
// app would read it.
func checkSecretRefs(ctx context.Context, old, env map[string]string) error {
	a, ok := ActorFrom(ctx)
	if !ok || a.Admin() {
		return nil
	}
	had := secretRefs(old, nil)
	for _, ref := range secretRefs(env, nil) {
		if !slices.Contains(had, ref) {
			return RequireFeature(ctx, FeatureSecretProviders)
		}
	}
	return nil
}

// checkFeature is RequireFeature for work that may run without an actor.
func checkFeature(ctx context.Context, f Feature) error {
	if _, ok := ActorFrom(ctx); !ok {
		return nil
	}
	return RequireFeature(ctx, f)
}

// grantCreator gives a member who created a project every permission on
// it, for this request too.
func (c *Core) grantCreator(ctx context.Context, projectID string) error {
	a, ok := ActorFrom(ctx)
	if !ok || a.Admin() {
		return nil
	}
	perms := make([]string, len(Perms))
	for i, p := range Perms {
		perms[i] = string(p)
	}
	if err := c.store.SetGrant(ctx, store.ProjectGrant{UserID: a.UserID, ProjectID: projectID, Perms: perms}); err != nil {
		return err
	}
	if a.Grants != nil {
		a.Grants[projectID] = perms
	}
	return nil
}

// requireOn checks the actor's token scope and its permission on the
// service's project.
func (c *Core) requireOn(ctx context.Context, serviceID string, scope store.Scope, p Perm) error {
	if err := Require(ctx, scope); err != nil {
		return err
	}
	projectID, err := c.ProjectOf(ctx, IDService, serviceID)
	if err != nil {
		return err
	}
	return Authorize(ctx, projectID, p)
}
