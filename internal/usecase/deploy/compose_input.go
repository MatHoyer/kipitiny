package deploy

import (
	"context"
	"fmt"
	"maps"
	"reflect"
	"slices"

	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/usecase/compose"
	"github.com/kipit-labs/kipitiny/internal/usecase/data"
	"github.com/kipit-labs/kipitiny/internal/usecase/monitoring"
	"github.com/kipit-labs/kipitiny/internal/usecase/network"
)

// input turns a compose service into a ServiceInput.
func (a *applier) input(name string, cs compose.Service) (ServiceInput, error) {
	in := ServiceInput{
		Name:             name,
		Kind:             domain.ServiceKind(cs.X.Kind),
		Image:            cs.Image,
		Icon:             cs.X.Icon,
		Tags:             cs.X.Tags,
		Replicas:         cs.Replicas,
		Port:             cs.X.Port,
		Domain:           cs.X.Domain,
		Analytics:        cs.X.Analytics,
		Errors:           cs.X.Errors,
		Env:              cs.Environment,
		Secrets:          cs.X.Secrets,
		MemoryMB:         cs.MemoryMB,
		CPUs:             cs.CPUs,
		HealthPath:       cs.X.HealthPath,
		PreDeploy:        cs.X.PreDeploy,
		Volumes:          cs.Volumes,
		PreBackup:        cs.X.PreBackup,
		PublishedPorts:   cs.Ports,
		Routes:           storeRoutes(cs.X.Routes),
		StopGraceSeconds: cs.StopGraceSeconds,
		HostNetwork:      cs.HostNetwork,
		DockerSocket:     cs.DockerSocket,
		Preview:          cs.X.Preview,
	}
	if in.Kind == "" {
		in.Kind = domain.ServiceKindApp
	}
	if cs.X.Anonymize != nil {
		in.Anonymize = *cs.X.Anonymize
	}
	if in.Replicas == 0 {
		in.Replicas = 1
	}
	if in.Env == nil {
		in.Env = map[string]string{}
	}
	if cs.Healthcheck != nil {
		in.Healthcheck = *cs.Healthcheck
	}
	if cs.Runtime != nil {
		in.Runtime = *cs.Runtime
	}
	in.Files, in.DependsOn, in.ProjectNetworks = cs.Files, cs.DependsOn, cs.Networks
	if in.Secrets == nil {
		// Without x-kipitiny.secrets, a value given as a whole ${NAME} is a
		// secret, unless it became a reference (a project variable keeps
		// its own flag).
		in.Secrets = []string{}
		for _, k := range cs.VarEnv {
			if v, ok := in.Env[k]; ok && !domain.SoleRefRe.MatchString(v) {
				in.Secrets = append(in.Secrets, k)
			}
		}
	}
	if d, ok := a.resolve(cs.X.Domain); ok {
		in.Domain = d
	} else {
		return ServiceInput{}, fmt.Errorf("service %s: domain: %s is not set", name, cs.X.Domain)
	}
	if cs.X.Password != "" {
		if pw, ok := a.resolve(cs.X.Password); ok {
			in.Password = pw
		}
	}
	if m := cs.X.Middlewares; m != nil {
		mw := &network.MiddlewaresInput{IPAllowList: m.IPAllowList, Headers: m.Headers}
		if m.RateLimit != nil {
			mw.RateLimit = &domain.RateLimit{Average: m.RateLimit.Average, Burst: m.RateLimit.Burst}
		}
		for _, u := range m.BasicAuth {
			bu := network.BasicAuthInput{Name: u.Name, Password: u.Ref}
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
		in.Middlewares = &network.MiddlewaresInput{}
	}
	return in, nil
}

func hasBasicAuthUser(s domain.Service, name string) bool {
	return slices.ContainsFunc(s.Middlewares.BasicAuth, func(u domain.BasicAuthUser) bool { return u.Name == name })
}

func (a *applier) prepareService(ctx context.Context, name string, in ServiceInput, project domain.Project, dbs map[string]domain.Service) error {
	old, exists := a.byName[name]
	if !exists {
		svc, err := a.c.serviceFromInput(a.project.ID, in)
		if err != nil {
			return err
		}
		// The project may not exist yet (a template's dry run): checks
		// find its server here, not in the store.
		svc.ServerID = a.project.ServerID
		if err := a.checkChange(ctx, domain.Service{ProjectID: a.project.ID, Env: map[string]string{}, Secrets: []string{}}, svc); err != nil {
			return err
		}
		if err := a.c.validateIn(ctx, svc, project, dbs); err != nil {
			return err
		}
		if err := a.checkPlatform(ctx, name, svc); err != nil {
			return err
		}
		a.planned[name] = svc
		if u := a.uptimes[name]; u != nil {
			chk, err := monitoring.UptimeCheck(svc, uptimeInput(*u))
			if err != nil {
				return err
			}
			a.uptimeOps = append(a.uptimeOps, uptimeOp{name: name, check: &chk})
		}
		if _, err := a.prepareJobs(ctx, name, svc, nil); err != nil {
			return err
		}
		a.plan.Create = append(a.plan.Create, name)
		a.creates = append(a.creates, in)
		return nil
	}
	a.planned[name] = old
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
	for _, k := range data.DatabaseNameKeys {
		if v := in.Env[k]; in.Kind.IsDatabase() && v != "" && v != old.Env[k] {
			a.warn("service %s: %s is fixed at creation; %s kept", name, k, old.Env[k])
		}
	}
	svc, err := a.c.patched(old, patch)
	if err != nil {
		return err
	}
	if err := a.checkChange(ctx, old, svc); err != nil {
		return err
	}
	if err := a.c.validateIn(ctx, svc, project, dbs); err != nil {
		return err
	}
	if svc.Image != old.Image || svc.Runtime.Platform != old.Runtime.Platform {
		if err := a.checkPlatform(ctx, name, svc); err != nil {
			return err
		}
	}
	a.planned[name] = svc
	fields := changedFields(old, svc)
	if in.networks != nil && !slices.Equal(slices.Sorted(slices.Values(old.Networks)), in.networks) {
		fields = append(fields, "networks")
		a.networkOps = append(a.networkOps, networkOp{id: old.ID, name: name, ids: in.networks})
	}
	uptimeChanged, err := a.prepareUptime(ctx, name, svc)
	if err != nil {
		return err
	}
	cur, err := a.c.store.ListJobs(ctx, old.ID)
	if err != nil {
		return err
	}
	jobsChanged, err := a.prepareJobs(ctx, name, svc, cur)
	if err != nil {
		return err
	}
	if len(fields) == 0 && !uptimeChanged && !jobsChanged {
		a.plan.Unchanged = append(a.plan.Unchanged, name)
		return nil
	}
	if len(fields) > 0 {
		// Scaling, the icon, analytics, sleep mode and the start order apply without a
		// deploy.
		needsDeploy := slices.ContainsFunc(fields, func(f string) bool {
			return f != "replicas" && f != "icon" && f != "tags" && f != "anonymize" && f != "analytics" && f != "sleepAfter" && f != "dependsOn" && f != "networks" &&
				(f != "files" || !old.Kind.IsDatabase()) // init scripts run on an empty database only
		})
		a.updates = append(a.updates, pendingUpdate{id: old.ID, name: name, patch: patch, deploy: needsDeploy})
	}
	if uptimeChanged {
		fields = append(fields, "uptime")
	}
	if jobsChanged {
		fields = append(fields, "jobs")
	}
	a.plan.Update = append(a.plan.Update, ServiceChange{Name: name, Fields: fields})
	return nil
}

// patchFromInput sets every field a compose file describes. A database's
// environment is generated, so it's left alone; its memory too when the
// file doesn't set it.
func patchFromInput(old domain.Service, in ServiceInput) ServicePatch {
	p := ServicePatch{
		Image:            &in.Image,
		Icon:             &in.Icon,
		Tags:             in.Tags,
		Anonymize:        &in.Anonymize,
		Replicas:         &in.Replicas,
		Port:             &in.Port,
		Domain:           &in.Domain,
		Analytics:        &in.Analytics,
		Errors:           &in.Errors,
		MemoryMB:         &in.MemoryMB,
		CPUs:             &in.CPUs,
		HealthPath:       &in.HealthPath,
		Healthcheck:      &in.Healthcheck,
		PreDeploy:        &in.PreDeploy,
		Volumes:          in.Volumes,
		Middlewares:      in.Middlewares,
		PreBackup:        &in.PreBackup,
		PublishedPorts:   in.PublishedPorts,
		Routes:           in.Routes,
		StopGraceSeconds: &in.StopGraceSeconds,
		HostNetwork:      &in.HostNetwork,
		DockerSocket:     &in.DockerSocket,
		Runtime:          &in.Runtime,
		Files:            in.Files,
		DependsOn:        in.DependsOn,
		ProjectNetworks:  in.ProjectNetworks,
		Preview:          &in.Preview,
	}
	if in.Image == "" { // a database on the default image
		p.Image = nil
	}
	if p.Volumes == nil {
		p.Volumes = []domain.Volume{}
	}
	if p.PublishedPorts == nil {
		p.PublishedPorts = []domain.PublishedPort{}
	}
	if p.Routes == nil {
		p.Routes = []domain.Route{}
	}
	if p.Tags == nil {
		p.Tags = []string{}
	}
	if p.Files == nil {
		p.Files = []domain.ServiceFile{}
	}
	if p.DependsOn == nil {
		p.DependsOn = []domain.Dependency{}
	}
	if p.ProjectNetworks == nil {
		p.ProjectNetworks = []string{}
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
func changedFields(old, svc domain.Service) []string {
	var out []string
	add := func(name string, changed bool) {
		if changed {
			out = append(out, name)
		}
	}
	add("image", old.Image != svc.Image)
	add("icon", old.Icon != svc.Icon)
	add("tags", !slices.Equal(old.Tags, svc.Tags))
	add("anonymize", (old.Anonymize == nil) != (svc.Anonymize == nil) || old.Anonymize.Hash("") != svc.Anonymize.Hash(""))
	add("replicas", old.Replicas != svc.Replicas)
	add("port", old.Port != svc.Port)
	add("domain", old.Domain != svc.Domain)
	add("analytics", old.Analytics != svc.Analytics)
	add("errors", old.Errors != svc.Errors)
	add("env", !maps.Equal(old.Env, svc.Env) || !slices.Equal(old.Secrets, svc.Secrets))
	add("memory", old.MemoryMB != svc.MemoryMB)
	add("cpus", old.CPUs != svc.CPUs)
	add("healthPath", old.HealthPath != svc.HealthPath)
	add("healthcheck", !slices.Equal(old.Healthcheck.Test, svc.Healthcheck.Test) ||
		old.Healthcheck.IntervalSeconds != svc.Healthcheck.IntervalSeconds || old.Healthcheck.TimeoutSeconds != svc.Healthcheck.TimeoutSeconds ||
		old.Healthcheck.StartPeriodSeconds != svc.Healthcheck.StartPeriodSeconds || old.Healthcheck.Retries != svc.Healthcheck.Retries)
	add("preDeploy", old.PreDeploy != svc.PreDeploy)
	add("preBackup", old.PreBackup != svc.PreBackup)
	add("volumes", !reflect.DeepEqual(old.Volumes, svc.Volumes))
	add("publishedPorts", !slices.Equal(old.PublishedPorts, svc.PublishedPorts))
	add("routes", !network.SameRoutes(old.Routes, svc.Routes))
	add("stopGraceSeconds", old.StopGraceSeconds != svc.StopGraceSeconds)
	add("hostNetwork", old.HostNetwork != svc.HostNetwork)
	add("dockerSocket", old.DockerSocket != svc.DockerSocket)
	add("runtime", !reflect.DeepEqual(old.Runtime, svc.Runtime))
	add("files", !sameFiles(old.Files, svc.Files))
	add("dependsOn", !slices.Equal(old.DependsOn, svc.DependsOn))
	add("projectNetworks", !slices.Equal(old.ProjectNetworks, svc.ProjectNetworks))
	add("middlewares", !sameMiddlewares(old.Middlewares, svc.Middlewares))
	add("preview", old.Preview != svc.Preview)
	return out
}

func composeRoutes(rs []domain.Route) []compose.Route {
	var out []compose.Route
	for _, r := range rs {
		out = append(out, compose.Route{Paths: r.Paths, Scheme: r.Scheme, Port: r.Port})
	}
	return out
}

func storeRoutes(rs []compose.Route) []domain.Route {
	var out []domain.Route
	for _, r := range rs {
		out = append(out, domain.Route{Paths: r.Paths, Scheme: r.Scheme, Port: r.Port})
	}
	return out
}

func sameMiddlewares(a, b domain.Middlewares) bool {
	return slices.Equal(a.BasicAuth, b.BasicAuth) && slices.Equal(a.IPAllowList, b.IPAllowList) &&
		maps.Equal(a.Headers, b.Headers) &&
		(a.RateLimit == nil) == (b.RateLimit == nil) && (a.RateLimit == nil || *a.RateLimit == *b.RateLimit)
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
