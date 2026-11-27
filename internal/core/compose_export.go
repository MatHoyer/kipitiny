package core

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/kipit-labs/kipitiny/internal/compose"
	"github.com/kipit-labs/kipitiny/internal/domain"
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
		i := slices.IndexFunc(svcs, func(s domain.Service) bool { return s.Name == opts.Service })
		if i < 0 {
			return Export{}, fmt.Errorf("%w: service %q", domain.ErrNotFound, opts.Service)
		}
		svcs = svcs[i : i+1]
	}
	checks, err := c.store.ListUptimeChecks(ctx)
	if err != nil {
		return Export{}, err
	}
	byService := map[string]domain.UptimeCheck{}
	for _, chk := range checks {
		byService[chk.ServiceID] = chk
	}
	nets, err := c.store.ListNetworks(ctx)
	if err != nil {
		return Export{}, err
	}
	all, err := c.store.ListJobs(ctx, "")
	if err != nil {
		return Export{}, err
	}
	jobs := map[string][]domain.Job{}
	for _, j := range all {
		jobs[j.ServiceID] = append(jobs[j.ServiceID], j)
	}
	f, env := exportFile(p, svcs, byService, jobs, nets, opts.Service == "")
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

// exportFile maps the services, their uptime checks and jobs (by service
// ID) to compose, with the variables their secrets became. Networks created by
// hand (nets) become external networks. withProject adds the project's
// shared variables.
func exportFile(p domain.Project, svcs []domain.Service, checks map[string]domain.UptimeCheck, jobs map[string][]domain.Job, nets []domain.Network,
	withProject bool) (compose.File, map[string]string) {
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
			HostNetwork:      s.HostNetwork,
			DockerSocket:     s.DockerSocket,
			X: compose.Ext{
				Domain:     s.Domain,
				Port:       s.Port,
				HealthPath: s.HealthPath,
				PreDeploy:  s.PreDeploy,
				PreBackup:  s.PreBackup,
				Icon:       s.Icon,
				Tags:       s.Tags,
				Analytics:  s.Analytics,
				Errors:     s.Errors,
				Preview:    s.Preview,
				Routes:     composeRoutes(s.Routes),
			},
		}
		if s.Anonymize != nil {
			cs.X.Anonymize = &s.Anonymize
		}
		if s.Replicas > 1 {
			cs.Replicas = s.Replicas
		}
		if s.Healthcheck.Set() {
			h := s.Healthcheck
			cs.Healthcheck = &h
		}
		if !s.Runtime.IsZero() {
			r := compose.EscapeRuntime(s.Runtime)
			cs.Runtime = &r
		}
		for _, f := range s.Files {
			if f.Secret { // written as the variable it comes from
				name := varName(s.Name, f.Source)
				secret(name, string(f.Content))
				f.Content = []byte(name)
			} else {
				f.Content = []byte(compose.Escape(string(f.Content)))
			}
			f.Size = 0
			cs.Files = append(cs.Files, f)
		}
		for _, d := range s.DependsOn { // not one deleted since
			if slices.ContainsFunc(svcs, func(o domain.Service) bool { return o.Name == d.Service }) {
				cs.DependsOn = append(cs.DependsOn, d)
			}
		}
		for _, id := range s.Networks {
			if i := slices.IndexFunc(nets, func(n domain.Network) bool { return n.ID == id }); i >= 0 {
				cs.ExternalNetworks = append(cs.ExternalNetworks, nets[i].Name)
			}
		}
		if len(s.ProjectNetworks) > 0 || len(cs.ExternalNetworks) > 0 {
			cs.Networks = s.ProjectNetworks
			if len(cs.Networks) == 0 {
				cs.Networks = []string{"default"}
			}
		}
		for _, v := range s.Volumes {
			if volumeUsers[v.Name] > 1 {
				v.Name = s.Name + "-" + v.Name
			}
			cs.Volumes = append(cs.Volumes, v)
		}
		switch {
		case s.Kind.IsDatabase():
			cs.X.Kind = string(s.Kind)
			cs.X.Password = secret(varName(s.Name, "PASSWORD"), s.Env[databasePasswordKey(s.Kind)])
			for _, k := range databaseNameKeys { // to recreate it alike
				if v, ok := s.Env[k]; ok {
					if cs.Environment == nil {
						cs.Environment = map[string]string{}
					}
					cs.Environment[k] = v
				}
			}
		default:
			if len(s.Env) > 0 {
				cs.Environment = map[string]string{}
			}
			// The secrets written as ${NAME} are secrets by default on import:
			// x-kipitiny.secrets only lists them when that isn't enough (a
			// secret holding a reference).
			implicit := true
			for k, v := range s.Env {
				if slices.Contains(s.Secrets, k) && !soleRefRe.MatchString(v) {
					cs.Environment[k] = secret(varName(s.Name, k), v)
				} else {
					cs.Environment[k] = compose.Escape(v)
					implicit = implicit && !slices.Contains(s.Secrets, k)
				}
			}
			if !implicit {
				cs.X.Secrets = slices.Clone(s.Secrets)
			}
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
		if chk, ok := checks[s.ID]; ok {
			cs.X.Uptime = composeUptime(chk)
		}
		for _, j := range jobs[s.ID] {
			if cs.X.Jobs == nil {
				cs.X.Jobs = map[string]compose.Job{}
			}
			cj := compose.Job{Command: j.Command, Schedule: j.Cron, Paused: !j.Enabled}
			if j.TimeoutSec != defaultJobTimeout {
				cj.Timeout = compose.Seconds(j.TimeoutSec)
			}
			cs.X.Jobs[j.Name] = cj
		}
		f.Services[s.Name] = cs
	}
	return f, env
}

// composeUptime writes a check, leaving out the defaults.
func composeUptime(chk domain.UptimeCheck) *compose.Uptime {
	u := &compose.Uptime{ExpectedStatus: chk.ExpectedStatus, Paused: !chk.Enabled}
	if chk.Path != "/" {
		u.Path = chk.Path
	}
	if chk.IntervalSec != defaultUptimeInterval {
		u.Interval = compose.Seconds(chk.IntervalSec)
	}
	if chk.TimeoutSec != defaultUptimeTimeout {
		u.Timeout = compose.Seconds(chk.TimeoutSec)
	}
	return u
}

func uptimeInput(u compose.Uptime) UptimeInput {
	return UptimeInput{Path: u.Path, IntervalSec: int(u.Interval), TimeoutSec: int(u.Timeout), ExpectedStatus: u.ExpectedStatus, Enabled: !u.Paused}
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
	if _, err := c.ConfirmedUser(ctx, password); err != nil {
		return Export{}, err
	}
	return c.ExportCompose(ctx, projectID, ExportOptions{Service: service, Secrets: true})
}
