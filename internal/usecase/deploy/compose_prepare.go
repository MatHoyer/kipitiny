package deploy

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/kipit-labs/kipitiny/internal/usecase/data"
	"github.com/kipit-labs/kipitiny/internal/usecase/identity"
	"github.com/kipit-labs/kipitiny/internal/usecase/monitoring"

	"github.com/moby/moby/client"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/kipit-labs/kipitiny/internal/compose"
	"github.com/kipit-labs/kipitiny/internal/docker"
	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/usecase/network"
)

// applier plans then applies one compose file.
type applier struct {
	c       *Deployer
	project domain.Project
	opts    ApplyOptions
	git     bool
	byName  map[string]domain.Service
	plan    ComposePlan

	// hashes and images are each listed service's compose block hash and
	// image; created maps new services to their ID.
	hashes  map[string]string
	images  map[string]string
	created map[string]string
	// planned are the project's services as the file leaves them, by name.
	planned map[string]domain.Service

	// projectEnv and projectSecrets are the project's variables once applied.
	projectEnv     map[string]string
	projectSecrets []string
	creates        []ServiceInput
	updates        []pendingUpdate
	deletes        []domain.Service
	// uptimes are the listed services' checks in the file; uptimeOps the
	// checks to save or delete.
	uptimes   map[string]*compose.Uptime
	uptimeOps []uptimeOp
	// jobs are the listed services' jobs in the file; jobOps the jobs to
	// save or delete.
	jobs   map[string]map[string]compose.Job
	jobOps []jobOp
	// networkOps set the networks created by hand of existing services.
	networkOps []networkOp
}

type networkOp struct {
	id, name string
	ids      []string
}

// jobOp saves a service's job (an update when it has an ID), or deletes it.
type jobOp struct {
	name string
	job  domain.Job
	del  bool
}

// uptimeOp saves a service's check, or deletes it when check is nil.
type uptimeOp struct {
	name  string
	check *domain.UptimeCheck
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
	dbs := map[string]domain.Service{}
	for name, s := range a.byName {
		if s.Kind.IsDatabase() {
			dbs[name] = s
		}
	}
	nets, err := a.c.store.ListNetworks(ctx)
	if err != nil {
		return err
	}
	inputs := map[string]ServiceInput{}
	var errs []string
	for _, name := range f.ServiceNames() {
		if f.Services[name].X.Preview == "only" {
			a.warn("service %s runs in previews only", name)
			if a.git { // previews read the file: nothing to keep
				continue
			}
		}
		in, err := a.input(name, f.Services[name])
		if err == nil {
			if in.networks, err = externalNetworks(f.Services[name], nets, a.project.ServerID); err != nil {
				err = fmt.Errorf("service %s: %w", name, err)
			}
		}
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		inputs[name] = in
		a.hashes[name] = network.SpecHash(f.Services[name])
		a.images[name] = in.Image
		a.uptimes[name] = f.Services[name].X.Uptime
		a.jobs[name] = f.Services[name].X.Jobs
		if len(a.jobs[name]) > 0 && a.project.ParentID != "" {
			a.warn("service %s: jobs don't run in previews", name)
			a.jobs[name] = nil
		}
		if in.Kind.IsDatabase() {
			if _, ok := dbs[name]; !ok {
				dbs[name] = domain.Service{Name: name, Kind: in.Kind, Env: map[string]string{}}
			}
		}
	}
	for _, name := range f.ServiceNames() {
		in, ok := inputs[name]
		if !ok {
			continue
		}
		if err := a.prepareService(ctx, name, in, prospect, dbs); errors.Is(err, identity.ErrForbidden) {
			// The apply would be refused, not invalid: answer as it would.
			return fmt.Errorf("%w: service %s: %s", identity.ErrForbidden, name, strings.TrimPrefix(err.Error(), identity.ErrForbidden.Error()+": "))
		} else if err != nil {
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
	if len(errs) == 0 {
		if err := a.checkDomains(ctx); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%w: %s", ErrInvalid, strings.Join(errs, "; "))
	}
	return nil
}

// checkDomains checks the domains of the project's services as the file
// leaves them, against each other and the other projects' services.
func (a *applier) checkDomains(ctx context.Context) error {
	for name, s := range a.byName {
		if _, listed := a.planned[name]; !listed && !slices.ContainsFunc(a.deletes, func(d domain.Service) bool { return d.ID == s.ID }) {
			a.planned[name] = s // not in the file, kept
		}
	}
	all, err := a.c.store.ListAllServices(ctx)
	if err != nil {
		return err
	}
	others := slices.DeleteFunc(all, func(o domain.Service) bool { return o.ProjectID == a.project.ID })
	// Each pair once: a service is checked against the ones before it.
	for _, name := range slices.Sorted(maps.Keys(a.planned)) {
		s := a.planned[name]
		if err := a.c.mgr.CheckDomain(ctx, s, a.project.ServerID, others); err != nil {
			return fmt.Errorf("service %s: %s", name, strings.TrimPrefix(err.Error(), ErrInvalid.Error()+": "))
		}
		others = append(others, s)
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

// checkChange runs the permission checks of creating or updating a service
// while planning, so a dry run refuses what the apply would. A template's
// new project doesn't exist yet: its install checks them.
func (a *applier) checkChange(ctx context.Context, old, svc domain.Service) error {
	if a.project.ID == "" {
		return nil
	}
	return checkServiceChange(ctx, old, svc)
}

// checkPlatform warns when svc's platform isn't the server's (it runs
// under emulation) and refuses one its image doesn't ship. A server or
// registry that doesn't answer skips the check: the pull would tell.
func (a *applier) checkPlatform(ctx context.Context, name string, svc domain.Service) error {
	want := docker.Platform(svc.Runtime.Platform)
	dk := a.c.mgr.DockerFor(svc.ServerID)
	if want == nil || dk == nil {
		return nil
	}
	if info, err := dk.Info(ctx); err == nil && info.Arch != want.Architecture {
		a.warn("service %s: platform %s isn't the server's (%s/%s): it runs under emulation, slowly, and needs QEMU (binfmt) on the server",
			name, svc.Runtime.Platform, info.OS, info.Arch)
	}
	res, err := dk.DistributionInspect(ctx, svc.Image, client.DistributionInspectOptions{EncodedRegistryAuth: a.c.RegistryAuth(ctx, svc.Image)})
	// Without the image's platform list (unreachable registry, single
	// platform image), there's nothing to check.
	if others := otherPlatforms(res.Platforms, *want); err == nil && len(res.Platforms) > 0 && others != nil {
		return fmt.Errorf("%w: image %s has no %s platform (it has %s)", ErrInvalid, svc.Image, svc.Runtime.Platform, strings.Join(others, ", "))
	}
	return nil
}

// otherPlatforms lists an image's platforms when none is want; nil when one
// is (arm64 is arm64/v8).
func otherPlatforms(have []ocispec.Platform, want ocispec.Platform) []string {
	others := []string{}
	for _, p := range have {
		if p.OS == want.OS && p.Architecture == want.Architecture && (want.Variant == p.Variant || want.Variant == "" || (want.Variant == "v8" && p.Variant == "")) {
			return nil
		}
		if p.OS != "unknown" { // attestations
			others = append(others, path.Join(p.OS, p.Architecture, p.Variant))
		}
	}
	return others
}

// prepareUptime plans the check of an existing service, as svc will be:
// the file's, or none when the file has none. It reports a change.
func (a *applier) prepareUptime(ctx context.Context, name string, svc domain.Service) (bool, error) {
	cur, err := a.c.store.GetUptimeCheck(ctx, svc.ID)
	has := err == nil
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return false, err
	}
	u := a.uptimes[name]
	if u == nil {
		if has {
			a.uptimeOps = append(a.uptimeOps, uptimeOp{name: name})
		}
		return has, nil
	}
	chk, err := monitoring.UptimeCheck(svc, uptimeInput(*u))
	if err != nil {
		return false, err
	}
	if has && monitoring.SameUptime(cur, chk) {
		return false, nil
	}
	a.uptimeOps = append(a.uptimeOps, uptimeOp{name: name, check: &chk})
	return true, nil
}

// prepareJobs plans the jobs of a service, as svc will be, from the file's
// and the current ones (cur): the file's replace them. It reports a change.
func (a *applier) prepareJobs(_ context.Context, name string, svc domain.Service, cur []domain.Job) (bool, error) {
	want := a.jobs[name]
	if len(want) > 0 && svc.Kind != domain.ServiceKindApp {
		return false, fmt.Errorf("%w: service %s: only apps run jobs", ErrInvalid, name)
	}
	changed := false
	for _, jobName := range slices.Sorted(maps.Keys(want)) {
		cj := want[jobName]
		j := domain.Job{ServiceID: svc.ID}
		if i := slices.IndexFunc(cur, func(j domain.Job) bool { return j.Name == jobName }); i >= 0 {
			j = cur[i]
		}
		was := j
		in := JobInput{Name: jobName, Command: cj.Command, Cron: cj.Schedule, TimeoutSec: int(cj.Timeout), Enabled: !cj.Paused}
		if err := ApplyJobInput(&j, in); err != nil {
			return false, fmt.Errorf("service %s: job %s: %w", name, jobName, err)
		}
		if j.ID == "" || j != was {
			a.jobOps = append(a.jobOps, jobOp{name: name, job: j})
			changed = true
		}
	}
	for _, j := range cur {
		if _, ok := want[j.Name]; !ok {
			a.jobOps = append(a.jobOps, jobOp{name: name, job: j, del: true})
			changed = true
		}
	}
	return changed, nil
}

// databasePasswordKey is the env key of the password apps connect with.
func databasePasswordKey(k domain.ServiceKind) string {
	switch k {
	case domain.ServiceKindRedis:
		return data.RedisPassword
	case domain.ServiceKindMySQL, domain.ServiceKindMariaDB:
		return data.MySQLPassword
	case domain.ServiceKindMongoDB:
		return data.MongoPassword
	}
	return data.PGPassword
}
