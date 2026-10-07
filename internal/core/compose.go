package core

import (
	"context"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/MatHoyer/kipitiny/internal/compose"
	"github.com/MatHoyer/kipitiny/internal/store"
)

// ExportOptions picks what ExportCompose writes.
type ExportOptions struct {
	// Service limits the export to the service of that name.
	Service string
	// Secrets also returns the secret values, as a .env file.
	Secrets bool
}

// Export is a project as a compose file. Secrets are ${NAME} variables in
// Compose; Env, when asked for, is the .env file giving their values.
type Export struct {
	Compose []byte
	Env     []byte
}

// ExportCompose writes a project (or one service) as an equivalent compose
// file. Secret values are never in Compose: each is a ${NAME} variable,
// valued in Env when opts.Secrets is set. References ({{ db.X.URL }},
// password manager entries) are written as they are.
func (c *Core) ExportCompose(ctx context.Context, projectID string, opts ExportOptions) (Export, error) {
	p, err := c.store.GetProject(ctx, projectID)
	if err != nil {
		return Export{}, err
	}
	svcs, err := c.store.ListServices(ctx, projectID)
	if err != nil {
		return Export{}, err
	}
	if opts.Service != "" {
		i := slices.IndexFunc(svcs, func(s store.Service) bool { return s.Name == opts.Service })
		if i < 0 {
			return Export{}, fmt.Errorf("%w: service %q", store.ErrNotFound, opts.Service)
		}
		svcs = svcs[i : i+1]
	}
	f, env := exportFile(p, svcs, opts.Service == "")
	data, err := compose.Marshal(f)
	if err != nil {
		return Export{}, err
	}
	out := Export{Compose: data}
	if opts.Secrets {
		out.Env = compose.MarshalEnv(env)
	}
	return out, nil
}

// exportFile maps the services to compose, with the variables their secrets
// became. withProject adds the project's shared variables.
func exportFile(p store.Project, svcs []store.Service, withProject bool) (compose.File, map[string]string) {
	f := compose.File{Name: p.Name, Services: map[string]compose.Service{}}
	env := map[string]string{}
	secret := func(name, value string) string {
		env[name] = value
		return "${" + name + "}"
	}
	if withProject && len(p.Env) > 0 {
		f.X.Variables = map[string]string{}
		for k, v := range p.Env {
			if slices.Contains(p.Secrets, k) && !soleRefRe.MatchString(v) {
				v = secret(varName("PROJECT", k), v)
			}
			f.X.Variables[k] = v
		}
		f.X.Secrets = slices.Clone(p.Secrets)
	}
	// Compose volumes are project-wide, kipitiny's are per service: a name
	// two services use gets the service as prefix.
	volumeUsers := map[string]int{}
	for _, s := range svcs {
		for _, v := range s.Volumes {
			volumeUsers[v.Name]++
		}
	}
	for _, s := range svcs {
		cs := compose.Service{
			Image:            s.Image,
			Ports:            s.PublishedPorts,
			MemoryMB:         s.MemoryMB,
			CPUs:             s.CPUs,
			StopGraceSeconds: s.StopGraceSeconds,
			X: compose.Ext{
				Domain:     s.Domain,
				Port:       s.Port,
				HealthPath: s.HealthPath,
				PreDeploy:  s.PreDeploy,
				PreBackup:  s.PreBackup,
				Icon:       s.Icon,
			},
		}
		if s.Replicas > 1 {
			cs.Replicas = s.Replicas
		}
		for _, v := range s.Volumes {
			if volumeUsers[v.Name] > 1 {
				v.Name = s.Name + "-" + v.Name
			}
			cs.Volumes = append(cs.Volumes, v)
		}
		switch s.Kind {
		case store.ServiceKindPostgres:
			cs.X.Kind = string(s.Kind)
			cs.X.Password = secret(varName(s.Name, "PASSWORD"), s.Env[pgPassword])
		case store.ServiceKindRedis:
			cs.X.Kind = string(s.Kind)
			cs.X.Password = secret(varName(s.Name, "PASSWORD"), s.Env[redisPassword])
		default:
			if len(s.Env) > 0 {
				cs.Environment = map[string]string{}
			}
			for k, v := range s.Env {
				if slices.Contains(s.Secrets, k) && !soleRefRe.MatchString(v) {
					cs.Environment[k] = secret(varName(s.Name, k), v)
				} else {
					cs.Environment[k] = compose.Escape(v)
				}
			}
			cs.X.Secrets = slices.Clone(s.Secrets)
		}
		if m := s.Middlewares; len(m.BasicAuth) > 0 || len(m.IPAllowList) > 0 || m.RateLimit != nil || len(m.Headers) > 0 {
			cm := &compose.Middlewares{IPAllowList: m.IPAllowList, Headers: m.Headers}
			if m.RateLimit != nil {
				cm.RateLimit = &compose.RateLimit{Average: m.RateLimit.Average, Burst: m.RateLimit.Burst}
			}
			for _, u := range m.BasicAuth {
				cu := compose.BasicAuthUser{Name: u.Name, Ref: u.Ref}
				if u.Ref == "" {
					cu.Hash = secret(varName(s.Name, "BASIC_AUTH_"+u.Name), u.Hash)
				}
				cm.BasicAuth = append(cm.BasicAuth, cu)
			}
			cs.X.Middlewares = cm
		}
		f.Services[s.Name] = cs
	}
	return f, env
}

var nonVarRe = regexp.MustCompile(`[^A-Z0-9]+`)

// varName is the .env variable of a secret: WEB_API_KEY for web's API_KEY.
func varName(prefix, key string) string {
	return nonVarRe.ReplaceAllString(strings.ToUpper(prefix+"_"+key), "_")
}

// ExportWithSecrets is ExportCompose with the secret values, for moving a
// project to another manager. The signed-in user must re-enter their
// password: the .env holds every credential of the project.
func (c *Core) ExportWithSecrets(ctx context.Context, projectID, service, password string) (Export, error) {
	if _, err := c.confirmedUser(ctx, password); err != nil {
		return Export{}, err
	}
	return c.ExportCompose(ctx, projectID, ExportOptions{Service: service, Secrets: true})
}

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
	byName := map[string]store.Service{}
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
			}
			return "{{ project." + name + " }}"
		},
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	a := &applier{c: c, project: project, opts: opts, git: git, byName: byName,
		hashes: map[string]string{}, images: map[string]string{}, created: map[string]string{}, plan: ComposePlan{
			Create: []string{}, Update: []ServiceChange{}, Unchanged: []string{}, Delete: []string{}, Orphaned: []string{},
			Variables: []string{}, Warnings: append([]string{}, warns...), Deploying: []string{},
		}}
	if err := a.prepare(ctx, f); err != nil {
		return a, err
	}
	if opts.DryRun {
		return a, nil
	}
	return a, a.apply(ctx)
}

// applier plans then applies one compose file.
type applier struct {
	c       *Core
	project store.Project
	opts    ApplyOptions
	git     bool
	byName  map[string]store.Service
	plan    ComposePlan

	// hashes and images are each listed service's compose block hash and
	// image; created maps new services to their ID.
	hashes  map[string]string
	images  map[string]string
	created map[string]string

	// projectEnv and projectSecrets are the project's variables once applied.
	projectEnv     map[string]string
	projectSecrets []string
	creates        []ServiceInput
	updates        []pendingUpdate
	deletes        []store.Service
}

type pendingUpdate struct {
	id     string
	name   string
	patch  ServicePatch
	deploy bool
}

func (a *applier) warn(format string, args ...any) {
	a.plan.Warnings = append(a.plan.Warnings, fmt.Sprintf(format, args...))
}

// resolve gives a ${NAME} value from the .env; ok false when it's a
// variable the .env doesn't set.
func (a *applier) resolve(v string) (string, bool) {
	name, isVar := compose.SoleVar(v)
	if !isVar {
		return v, true
	}
	val, ok := a.opts.Env[name]
	return val, ok
}

func (a *applier) prepare(ctx context.Context, f compose.File) error {
	if err := a.prepareVariables(f.X); err != nil {
		return err
	}
	prospect := a.project
	prospect.Env = a.projectEnv

	// Databases the services may reference: the ones kept and the new ones.
	dbs := map[string]store.Service{}
	for name, s := range a.byName {
		if s.Kind.IsDatabase() {
			dbs[name] = s
		}
	}
	inputs := map[string]ServiceInput{}
	var errs []string
	for _, name := range f.ServiceNames() {
		in, err := a.input(name, f.Services[name])
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		inputs[name] = in
		a.hashes[name] = specHash(f.Services[name])
		a.images[name] = in.Image
		if in.Kind.IsDatabase() {
			if _, ok := dbs[name]; !ok {
				dbs[name] = store.Service{Name: name, Kind: in.Kind, Env: map[string]string{}}
			}
		}
	}
	for _, name := range f.ServiceNames() {
		in, ok := inputs[name]
		if !ok {
			continue
		}
		if err := a.prepareService(ctx, name, in, prospect, dbs); err != nil {
			errs = append(errs, fmt.Sprintf("service %s: %v", name, err))
		}
	}
	for _, name := range slices.Sorted(maps.Keys(a.byName)) {
		if _, listed := f.Services[name]; listed {
			continue
		}
		s := a.byName[name]
		switch {
		case s.Kind.IsDatabase():
			a.plan.Orphaned = append(a.plan.Orphaned, name)
		case a.opts.Prune:
			a.plan.Delete = append(a.plan.Delete, name)
			a.deletes = append(a.deletes, s)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%w: %s", ErrInvalid, strings.Join(errs, "; "))
	}
	return nil
}

// prepareVariables merges the file's project variables over the current
// ones; variables the file doesn't list are kept.
func (a *applier) prepareVariables(x compose.FileExt) error {
	a.projectEnv = maps.Clone(a.project.Env)
	if a.projectEnv == nil {
		a.projectEnv = map[string]string{}
	}
	a.projectSecrets = slices.Clone(a.project.Secrets)
	var missing []string
	for _, k := range slices.Sorted(maps.Keys(x.Variables)) {
		v, ok := a.resolve(x.Variables[k])
		if !ok {
			// A secret exported without its value keeps the current one.
			if _, had := a.project.Env[k]; had {
				continue
			}
			missing = append(missing, k)
			continue
		}
		if a.project.Env[k] != v || slices.Contains(x.Secrets, k) != slices.Contains(a.project.Secrets, k) {
			a.plan.Variables = append(a.plan.Variables, k)
		}
		a.projectEnv[k] = v
		a.projectSecrets = slices.DeleteFunc(a.projectSecrets, func(s string) bool { return s == k })
		if slices.Contains(x.Secrets, k) {
			a.projectSecrets = append(a.projectSecrets, k)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: project variables without a value: %s", ErrInvalid, strings.Join(missing, ", "))
	}
	return nil
}

// input turns a compose service into a ServiceInput.
func (a *applier) input(name string, cs compose.Service) (ServiceInput, error) {
	in := ServiceInput{
		Name:             name,
		Kind:             store.ServiceKind(cs.X.Kind),
		Image:            cs.Image,
		Icon:             cs.X.Icon,
		Replicas:         cs.Replicas,
		Port:             cs.X.Port,
		Domain:           cs.X.Domain,
		Env:              cs.Environment,
		Secrets:          cs.X.Secrets,
		MemoryMB:         cs.MemoryMB,
		CPUs:             cs.CPUs,
		HealthPath:       cs.X.HealthPath,
		PreDeploy:        cs.X.PreDeploy,
		Volumes:          cs.Volumes,
		PreBackup:        cs.X.PreBackup,
		PublishedPorts:   cs.Ports,
		StopGraceSeconds: cs.StopGraceSeconds,
	}
	if in.Kind == "" {
		in.Kind = store.ServiceKindApp
	}
	if in.Replicas == 0 {
		in.Replicas = 1
	}
	if in.Env == nil {
		in.Env = map[string]string{}
	}
	if in.Secrets == nil {
		in.Secrets = []string{}
	}
	if cs.X.Password != "" {
		if pw, ok := a.resolve(cs.X.Password); ok {
			in.Password = pw
		}
	}
	if m := cs.X.Middlewares; m != nil {
		mw := &MiddlewaresInput{IPAllowList: m.IPAllowList, Headers: m.Headers}
		if m.RateLimit != nil {
			mw.RateLimit = &store.RateLimit{Average: m.RateLimit.Average, Burst: m.RateLimit.Burst}
		}
		for _, u := range m.BasicAuth {
			bu := BasicAuthInput{Name: u.Name, Password: u.Ref}
			if u.Ref == "" {
				h, ok := a.resolve(u.Hash)
				if !ok {
					// Exported without its value: keep the user's current hash.
					if !hasBasicAuthUser(a.byName[name], u.Name) {
						return ServiceInput{}, fmt.Errorf("service %s: basic auth user %s: %s is not set", name, u.Name, u.Hash)
					}
				}
				bu.Hash = h
				if !ok {
					bu.Hash = ""
				}
			}
			mw.BasicAuth = append(mw.BasicAuth, bu)
		}
		in.Middlewares = mw
	} else {
		in.Middlewares = &MiddlewaresInput{}
	}
	return in, nil
}

func hasBasicAuthUser(s store.Service, name string) bool {
	return slices.ContainsFunc(s.Middlewares.BasicAuth, func(u store.BasicAuthUser) bool { return u.Name == name })
}

func (a *applier) prepareService(ctx context.Context, name string, in ServiceInput, project store.Project, dbs map[string]store.Service) error {
	old, exists := a.byName[name]
	if !exists {
		svc, err := a.c.serviceFromInput(a.project.ID, in)
		if err != nil {
			return err
		}
		if err := a.c.validateIn(ctx, svc, project, dbs); err != nil {
			return err
		}
		a.plan.Create = append(a.plan.Create, name)
		a.creates = append(a.creates, in)
		return nil
	}
	if a.git && old.GitSpecHash == a.hashes[name] {
		// Unchanged in the file since the last sync: keep what was done
		// since (a tag deployed by CI).
		a.plan.Unchanged = append(a.plan.Unchanged, name)
		return nil
	}
	if old.Kind != in.Kind {
		return fmt.Errorf("%w: kind changes from %s to %s; delete the service first", ErrInvalid, old.Kind, in.Kind)
	}
	patch := patchFromInput(old, in)
	if in.Kind.IsDatabase() && in.Password != "" && in.Password != old.Env[databasePasswordKey(old.Kind)] {
		a.warn("service %s: a database's password is fixed at creation; kept", name)
	}
	svc, err := a.c.patched(old, patch)
	if err != nil {
		return err
	}
	if err := a.c.validateIn(ctx, svc, project, dbs); err != nil {
		return err
	}
	fields := changedFields(old, svc)
	if len(fields) == 0 {
		a.plan.Unchanged = append(a.plan.Unchanged, name)
		return nil
	}
	a.plan.Update = append(a.plan.Update, ServiceChange{Name: name, Fields: fields})
	// Scaling and the icon apply without a deploy.
	needsDeploy := slices.ContainsFunc(fields, func(f string) bool { return f != "replicas" && f != "icon" })
	a.updates = append(a.updates, pendingUpdate{id: old.ID, name: name, patch: patch, deploy: needsDeploy})
	return nil
}

func databasePasswordKey(k store.ServiceKind) string {
	if k == store.ServiceKindRedis {
		return redisPassword
	}
	return pgPassword
}

// patchFromInput sets every field a compose file describes. A database's
// environment is generated, so it's left alone; its memory too when the
// file doesn't set it.
func patchFromInput(old store.Service, in ServiceInput) ServicePatch {
	p := ServicePatch{
		Image:            &in.Image,
		Icon:             &in.Icon,
		Replicas:         &in.Replicas,
		Port:             &in.Port,
		Domain:           &in.Domain,
		MemoryMB:         &in.MemoryMB,
		CPUs:             &in.CPUs,
		HealthPath:       &in.HealthPath,
		PreDeploy:        &in.PreDeploy,
		Volumes:          in.Volumes,
		Middlewares:      in.Middlewares,
		PreBackup:        &in.PreBackup,
		PublishedPorts:   in.PublishedPorts,
		StopGraceSeconds: &in.StopGraceSeconds,
	}
	if in.Image == "" { // a database on the default image
		p.Image = nil
	}
	if p.Volumes == nil {
		p.Volumes = []store.Volume{}
	}
	if p.PublishedPorts == nil {
		p.PublishedPorts = []store.PublishedPort{}
	}
	if old.Kind.IsDatabase() {
		if in.MemoryMB == 0 {
			p.MemoryMB = nil
		}
		return p
	}
	p.Env, p.Secrets = in.Env, in.Secrets
	return p
}

// changedFields names the settings that differ between old and svc.
func changedFields(old, svc store.Service) []string {
	var out []string
	add := func(name string, changed bool) {
		if changed {
			out = append(out, name)
		}
	}
	add("image", old.Image != svc.Image)
	add("icon", old.Icon != svc.Icon)
	add("replicas", old.Replicas != svc.Replicas)
	add("port", old.Port != svc.Port)
	add("domain", old.Domain != svc.Domain)
	add("env", !maps.Equal(old.Env, svc.Env) || !slices.Equal(old.Secrets, svc.Secrets))
	add("memory", old.MemoryMB != svc.MemoryMB)
	add("cpus", old.CPUs != svc.CPUs)
	add("healthPath", old.HealthPath != svc.HealthPath)
	add("preDeploy", old.PreDeploy != svc.PreDeploy)
	add("preBackup", old.PreBackup != svc.PreBackup)
	add("volumes", !slices.Equal(old.Volumes, svc.Volumes))
	add("publishedPorts", !slices.Equal(old.PublishedPorts, svc.PublishedPorts))
	add("stopGraceSeconds", old.StopGraceSeconds != svc.StopGraceSeconds)
	add("middlewares", !sameMiddlewares(old.Middlewares, svc.Middlewares))
	return out
}

func sameMiddlewares(a, b store.Middlewares) bool {
	return slices.Equal(a.BasicAuth, b.BasicAuth) && slices.Equal(a.IPAllowList, b.IPAllowList) &&
		maps.Equal(a.Headers, b.Headers) &&
		(a.RateLimit == nil) == (b.RateLimit == nil) && (a.RateLimit == nil || *a.RateLimit == *b.RateLimit)
}

func (a *applier) apply(ctx context.Context) error {
	c := a.c
	if len(a.plan.Variables) > 0 {
		if _, err := c.SetProjectEnv(ctx, a.project.ID, a.projectEnv, a.projectSecrets); err != nil {
			return fmt.Errorf("project variables: %w", err)
		}
	}
	var dbs, apps []string // IDs to deploy
	queue := func(id string, kind store.ServiceKind) {
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
		queue(svc.ID, svc.Kind)
	}
	for _, u := range a.updates {
		svc, err := c.updateService(ctx, u.id, u.patch)
		if err != nil {
			return fmt.Errorf("update %s: %w", u.name, err)
		}
		if u.deploy && !svc.Stopped && svc.CurrentDeploymentID != "" {
			queue(svc.ID, svc.Kind)
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
		a.plan.Deploying = append(a.plan.Deploying, in.Name)
	}
	slices.Sort(a.plan.Deploying)
	commit := a.opts.Commit
	return c.goBackground(func() { c.deployInOrder(dbs, apps, commit) })
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

func boolCmp(x, y bool) int {
	switch {
	case x == y:
		return 0
	case !x:
		return -1
	}
	return 1
}

// deployInOrder deploys the databases, waits for them, then the apps, so a
// new app's pre-deploy (migrations) finds its database up.
func (c *Core) deployInOrder(dbs, apps []string, commit string) {
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
	deadline := time.Now().Add(deployTimeout)
	for _, id := range started {
		for time.Now().Before(deadline) {
			d, err := c.store.GetDeployment(ctx, id)
			if err != nil || d.Status != store.DeploymentRunning {
				break
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
		}
	}
	for _, id := range apps {
		if _, err := c.Deploy(ctx, id, DeployOptions{Commit: commit}); err != nil {
			c.log.Warn("compose deploy", "service", id, "err", err)
		}
	}
}
