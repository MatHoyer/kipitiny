package core

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/kipit-labs/kipitiny/internal/usecase/identity"

	"github.com/kipit-labs/kipitiny/internal/compose"
	"github.com/kipit-labs/kipitiny/internal/domain"
)

// ApplyOptions tunes ApplyCompose.
type ApplyOptions struct {
	// Env gives the file's ${NAME} variables their values (a .env file).
	// A missing variable that is a whole environment value becomes a
	// reference to the project variable of that name.
	Env map[string]string `json:"env"`
	// Prune deletes the apps the file doesn't list. Databases are only
	// reported (Orphaned): deleting one is always a manual step.
	Prune bool `json:"prune"`
	// DryRun returns the plan without changing anything.
	DryRun bool `json:"dryRun"`
	// Deploy deploys the created services and the changed ones, databases
	// first.
	Deploy bool `json:"deploy"`
	// Commit is the source revision, recorded on the deployments.
	Commit string `json:"commit,omitempty"`
	// files reads the git repository's files (compose.Vars.Files); nil
	// without one.
	files func(path string) ([]domain.ServiceFile, error)
	// afterDatabases runs once the databases deployed, before the apps,
	// with the services created by name (a preview's seed).
	afterDatabases func(ctx context.Context, created map[string]string)
}

// ComposePlan is what applying a compose file changes.
type ComposePlan struct {
	Create    []string        `json:"create"`
	Update    []ServiceChange `json:"update"`
	Unchanged []string        `json:"unchanged"`
	// Delete lists the apps pruned; Orphaned the databases the file no
	// longer lists, kept.
	Delete   []string `json:"delete"`
	Orphaned []string `json:"orphaned"`
	// Variables are the project variables the file sets.
	Variables []string `json:"variables"`
	Warnings  []string `json:"warnings"`
	// Deploying lists the services being deployed (not in a dry run).
	Deploying []string `json:"deploying"`
}

// ServiceChange names the settings of a service that change.
type ServiceChange struct {
	Name   string   `json:"name"`
	Fields []string `json:"fields"`
}

// composeLock serializes compose applies per project.
func composeLock(projectID string) string { return "compose:" + projectID }

// ApplyCompose makes the project match a compose file: creates the
// services it adds, updates the ones that differ and, with Prune, deletes
// the apps it drops. The whole file is checked before anything changes.
func (c *Core) ApplyCompose(ctx context.Context, projectID string, data []byte, opts ApplyOptions) (ComposePlan, error) {
	if c.isGitProject(ctx, projectID) {
		return ComposePlan{}, ErrGitManaged
	}
	a, err := c.applyCompose(ctx, projectID, data, opts, false)
	if a == nil {
		return ComposePlan{}, err
	}
	return a.plan, err
}

// applyCompose plans then, unless a dry run, applies a compose file. git
// marks a git sync: services whose compose block is unchanged since the
// last sync are left alone, and the sync state is recorded. The applier
// is returned with what was done, also on a failure while applying.
func (c *Core) applyCompose(ctx context.Context, projectID string, data []byte, opts ApplyOptions, git bool) (*applier, error) {
	unlock, err := c.lockService(composeLock(projectID))
	if err != nil {
		return nil, err
	}
	defer unlock()

	project, err := c.store.GetProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	existing, err := c.store.ListServices(ctx, projectID)
	if err != nil {
		return nil, err
	}
	a, err := c.planCompose(ctx, project, existing, data, opts, git)
	if err != nil {
		return a, err
	}
	// The compose route needs settings; a sync, run with the git permission,
	// needs what applying the file by hand would.
	if git && (len(a.plan.Create) > 0 || len(a.plan.Update) > 0) {
		if err := identity.Check(ctx, projectID, identity.PermSettings); err != nil {
			return a, err
		}
		if opts.Deploy {
			if err := identity.Check(ctx, projectID, identity.PermDeploy); err != nil {
				return a, err
			}
		}
	}
	if opts.DryRun {
		return a, nil
	}
	if len(a.deletes) > 0 { // pruning deletes apps with their data
		if err := identity.Check(ctx, projectID, identity.PermDelete); err != nil {
			return a, err
		}
	}
	if err := a.apply(ctx); err != nil {
		return a, err
	}
	if project.ParentID == "" { // rules may have changed
		c.logAnonymizeErr(c.refreshAnonymized(ctx, projectID, false))
	}
	return a, nil
}

// planCompose checks a compose file against a project and its services
// and plans the changes. The project may not exist yet (a template's dry
// run): then it has no ID and no services.
func (c *Core) planCompose(ctx context.Context, project domain.Project, existing []domain.Service, data []byte, opts ApplyOptions, git bool) (*applier, error) {
	byName := map[string]domain.Service{}
	for _, s := range existing {
		byName[s.Name] = s
	}
	f, warns, err := compose.Parse(data, compose.Vars{
		Lookup: func(name string) (string, bool) { v, ok := opts.Env[name]; return v, ok },
		Ref: func(service, key, name string) string {
			// A secret exported without its value keeps the current one.
			if old, ok := byName[service]; ok && name == varName(service, key) {
				if v, had := old.Env[key]; had {
					return v
				}
				if i := slices.IndexFunc(old.Files, func(f domain.ServiceFile) bool { return f.Source == key }); i >= 0 {
					return string(old.Files[i].Content)
				}
			}
			return "{{ project." + name + " }}"
		},
		Files: opts.files,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	a := &applier{c: c, project: project, opts: opts, git: git, byName: byName,
		hashes: map[string]string{}, images: map[string]string{}, created: map[string]string{}, planned: map[string]domain.Service{}, uptimes: map[string]*compose.Uptime{}, jobs: map[string]map[string]compose.Job{}, plan: ComposePlan{
			Create: []string{}, Update: []ServiceChange{}, Unchanged: []string{}, Delete: []string{}, Orphaned: []string{},
			Variables: []string{}, Warnings: append([]string{}, warns...), Deploying: []string{},
		}}
	return a, a.prepare(ctx, f)
}

func (a *applier) apply(ctx context.Context) error {
	c := a.c
	if len(a.plan.Variables) > 0 {
		if _, err := c.SetProjectEnv(ctx, a.project.ID, a.projectEnv, a.projectSecrets); err != nil {
			return fmt.Errorf("project variables: %w", err)
		}
	}
	var dbs, apps []string // IDs to deploy
	queue := func(id string, kind domain.ServiceKind) {
		if kind.IsDatabase() {
			dbs = append(dbs, id)
		} else {
			apps = append(apps, id)
		}
	}
	// Databases first: apps reference them.
	slices.SortStableFunc(a.creates, func(x, y ServiceInput) int {
		return boolCmp(!x.Kind.IsDatabase(), !y.Kind.IsDatabase())
	})
	for _, in := range a.creates {
		svc, err := c.CreateService(ctx, a.project.ID, in)
		if err != nil {
			return fmt.Errorf("create %s: %w", in.Name, err)
		}
		a.created[in.Name] = svc.ID
		if len(in.networks) > 0 {
			if err := c.store.SetServiceNetworks(ctx, svc.ID, in.networks); err != nil {
				return fmt.Errorf("networks of %s: %w", in.Name, err)
			}
		}
		if svc.Preview != "only" {
			queue(svc.ID, svc.Kind)
		}
	}
	for _, u := range a.updates {
		svc, err := c.updateService(ctx, u.id, u.patch)
		if err != nil {
			return fmt.Errorf("update %s: %w", u.name, err)
		}
		if needsDeploy(svc, u.deploy) {
			queue(svc.ID, svc.Kind)
		}
	}
	for _, name := range a.plan.Unchanged {
		if s, ok := a.byName[name]; ok && needsDeploy(s, false) {
			queue(s.ID, s.Kind)
		}
	}
	for _, op := range a.networkOps {
		if _, err := c.SetServiceNetworks(ctx, op.id, op.ids); err != nil {
			return fmt.Errorf("networks of %s: %w", op.name, err)
		}
	}
	for _, op := range a.uptimeOps {
		id := a.created[op.name]
		if id == "" {
			id = a.byName[op.name].ID
		}
		if op.check == nil {
			if err := c.RemoveUptime(ctx, id); err != nil && !errors.Is(err, domain.ErrNotFound) {
				return fmt.Errorf("uptime check of %s: %w", op.name, err)
			}
			continue
		}
		chk := *op.check
		chk.ServiceID = id
		if err := c.SaveUptime(ctx, chk); err != nil {
			return fmt.Errorf("uptime check of %s: %w", op.name, err)
		}
	}
	for _, op := range a.jobOps {
		var err error
		switch j := op.job; {
		case op.del:
			err = c.deleteJob(ctx, j)
		case j.ID != "":
			_, err = c.store.UpdateJob(ctx, j)
		default:
			if j.ServiceID == "" {
				j.ServiceID = a.created[op.name]
			}
			_, err = c.store.CreateJob(ctx, j)
		}
		if err != nil {
			return fmt.Errorf("job %s of %s: %w", op.job.Name, op.name, err)
		}
	}
	if len(a.jobOps) > 0 {
		if err := c.reloadJobs(ctx); err != nil {
			return err
		}
	}
	for _, s := range a.deletes {
		if err := c.DeleteService(ctx, s.ID, s.Name); err != nil {
			return fmt.Errorf("delete %s: %w", s.Name, err)
		}
	}
	if a.git {
		if err := a.recordGitState(ctx); err != nil {
			return err
		}
	}
	if !a.opts.Deploy || len(dbs)+len(apps) == 0 {
		return nil
	}
	for _, id := range append(slices.Clone(dbs), apps...) {
		for name, s := range a.byName {
			if s.ID == id {
				a.plan.Deploying = append(a.plan.Deploying, name)
			}
		}
	}
	for _, in := range a.creates {
		if in.Preview != "only" {
			a.plan.Deploying = append(a.plan.Deploying, in.Name)
		}
	}
	slices.Sort(a.plan.Deploying)
	commit, created := a.opts.Commit, maps.Clone(a.created)
	var between func()
	if hook := a.opts.afterDatabases; hook != nil {
		between = func() { hook(c.bg, created) }
	}
	return c.goBackground(func() { c.deployInOrder(dbs, apps, commit, between) })
}

// recordGitState stores each listed service's compose hash, and flags the
// databases the file dropped.
func (a *applier) recordGitState(ctx context.Context) error {
	for name, hash := range a.hashes {
		id := a.created[name]
		if id == "" {
			id = a.byName[name].ID
		}
		if err := a.c.store.SetServiceGitState(ctx, id, hash, false); err != nil {
			return err
		}
	}
	for _, name := range a.plan.Orphaned {
		s := a.byName[name]
		if err := a.c.store.SetServiceGitState(ctx, s.ID, s.GitSpecHash, true); err != nil {
			return err
		}
	}
	return nil
}

// deployInOrder deploys the databases, waits for them, then the apps, each
// once the deploys of those it depends on are done, so a new app's
// pre-deploy (migrations) finds its database up.
func (c *Core) deployInOrder(dbs, apps []string, commit string, between func()) {
	ctx := c.bg
	var started []string
	for _, id := range dbs {
		d, err := c.Deploy(ctx, id, DeployOptions{Commit: commit})
		if err != nil {
			c.log.Warn("compose deploy", "service", id, "err", err)
			continue
		}
		started = append(started, d.ID)
	}
	if !c.waitDeployments(ctx, started) {
		return
	}
	if between != nil {
		between()
	}
	var svcs []domain.Service
	for _, id := range apps {
		svc, err := c.store.GetService(ctx, id)
		if err != nil {
			c.log.Warn("compose deploy", "service", id, "err", err)
			continue
		}
		svcs = append(svcs, svc)
	}
	deployed := map[string]string{} // service name → deployment ID
	for _, svc := range startOrder(svcs) {
		var deps []string
		for _, d := range svc.DependsOn {
			if id, ok := deployed[d.Service]; ok {
				deps = append(deps, id)
			}
		}
		if !c.waitDeployments(ctx, deps) {
			return
		}
		if failed := c.failedDependency(ctx, svc, deployed); failed != "" {
			d, err := c.skipDeploy(ctx, svc, commit, fmt.Sprintf("not deployed: %s, which it depends on, wasn't deployed", failed))
			if err != nil {
				c.log.Warn("compose deploy", "service", svc.ID, "err", err)
				continue
			}
			deployed[svc.Name] = d.ID
			continue
		}
		d, err := c.Deploy(ctx, svc.ID, DeployOptions{Commit: commit})
		if err != nil {
			c.log.Warn("compose deploy", "service", svc.ID, "err", err)
			continue
		}
		deployed[svc.Name] = d.ID
	}
}

// needsDeploy reports whether an apply deploys an existing service: when
// its change needs it, or when it never deployed successfully (its first
// deploy failed, or a failed apply created it) even if its block is unchanged.
func needsDeploy(svc domain.Service, changed bool) bool {
	return svc.Preview != "only" && !svc.Stopped && (changed || svc.CurrentDeploymentID == "")
}

// waitDeployments waits until deployments are over, at most
// deployTimeout; false when ctx ends.
func (c *Core) waitDeployments(ctx context.Context, ids []string) bool {
	deadline := time.Now().Add(deployTimeout)
	for _, id := range ids {
		for time.Now().Before(deadline) {
			d, err := c.store.GetDeployment(ctx, id)
			if err != nil || d.Status != domain.DeploymentRunning {
				break
			}
			select {
			case <-ctx.Done():
				return false
			case <-time.After(2 * time.Second):
			}
		}
	}
	return true
}

// externalNetworks maps a service's external networks to the networks
// created by hand on server, by name; nil when the service lists none.
func externalNetworks(cs compose.Service, nets []domain.Network, server string) ([]string, error) {
	if cs.Networks == nil {
		return nil, nil
	}
	ids := []string{}
	for _, name := range cs.ExternalNetworks {
		i := slices.IndexFunc(nets, func(n domain.Network) bool { return n.Name == name && n.ServerID == server })
		if i < 0 {
			return nil, fmt.Errorf("network %s doesn't exist on the project's server: create it in Networks first", name)
		}
		ids = append(ids, nets[i].ID)
	}
	slices.Sort(ids)
	return ids, nil
}
