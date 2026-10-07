package core

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

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
