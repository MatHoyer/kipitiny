package deploy

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/kipit-labs/kipitiny/internal/usecase/data"
	"github.com/kipit-labs/kipitiny/internal/usecase/identity"

	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/usecase/network"
)

// dbFields are the credentials a database reference can name.
var dbFields = []string{"URL", "HOST", "PORT", "USER", "PASSWORD", "DATABASE"}

// envSources holds what references resolve against.
type envSources struct {
	project map[string]string
	// dbs are the project's database services by name.
	dbs map[string]domain.Service
	// secrets are password manager values by reference, fetched for a
	// deploy (resolveSecrets); nil when only checking references.
	secrets map[string]string
	// apps are the public URLs of the project's apps by name; nil when only
	// checking references before the apps exist (a compose file).
	apps map[string]string
	// errors are the Sentry SDK's variables a deploy adds (errorsEnv).
	errors map[string]string
}

// projectDatabases returns the project's database services by name.
func (c *Deployer) projectDatabases(ctx context.Context, projectID string) (map[string]domain.Service, error) {
	svcs, err := c.store.ListServices(ctx, projectID)
	if err != nil {
		return nil, err
	}
	return databasesOf(svcs), nil
}

func databasesOf(svcs []domain.Service) map[string]domain.Service {
	dbs := map[string]domain.Service{}
	for _, s := range svcs {
		if s.Kind.IsDatabase() {
			dbs[s.Name] = s
		}
	}
	return dbs
}

// appURLs returns the public URLs of the apps among svcs by name, with
// the HTTPS port when it isn't 443 (dev setups).
func (c *Deployer) appURLs(svcs []domain.Service) map[string]string {
	urls := map[string]string{}
	for _, s := range svcs {
		if s.Kind == domain.ServiceKindApp && network.HTTPRouted(s) {
			u := "https://" + s.Domain
			if p := c.cfg.Traefik.HTTPSPort; p != "" && p != "443" {
				u += ":" + p
			}
			urls[s.Name] = u
		}
	}
	return urls
}

// lookup resolves one domain.EnvRefRe match (its submatches).
func (src envSources) lookup(m []string) (string, bool) {
	if m[5] != "" {
		if src.apps == nil {
			return "", true
		}
		u, ok := src.apps[m[5]]
		if m[6] == "DOMAIN" {
			u = strings.TrimPrefix(u, "https://")
			u, _, _ = strings.Cut(u, ":")
		}
		return u, ok
	}
	if m[1] != "" {
		v, ok := src.project[m[1]]
		// A project entry may itself hold password manager and app
		// references.
		return src.expandSecrets(v), ok
	}
	if m[4] != "" {
		v, ok := src.secrets[m[4]]
		return v, ok || src.secrets == nil
	}
	db, ok := src.dbs[m[2]]
	if !ok {
		return "", false
	}
	return data.DatabaseField(db, m[3])
}

// expandSecrets substitutes the password manager and app references in
// v, leaving the others as they are.
func (src envSources) expandSecrets(v string) string {
	return domain.EnvRefRe.ReplaceAllStringFunc(v, func(ref string) string {
		m := domain.EnvRefRe.FindStringSubmatch(ref)
		switch {
		case m[4] != "" && src.secrets != nil:
			return src.secrets[m[4]]
		case m[5] != "" && src.apps != nil:
			v, _ := src.lookup(m)
			return v
		}
		return ref
	})
}

// secretRefs lists the password manager references env resolves through,
// directly or in the project entries it references, sorted.
func secretRefs(env map[string]string, project map[string]string) []string {
	var out []string
	add := func(v string) {
		for _, m := range domain.EnvRefRe.FindAllStringSubmatch(v, -1) {
			if m[4] != "" {
				out = append(out, m[4])
			}
		}
	}
	for _, v := range env {
		add(v)
		for _, m := range domain.EnvRefRe.FindAllStringSubmatch(v, -1) {
			if m[1] != "" {
				add(project[m[1]])
			}
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// checkSecretSchemes refuses references no password manager handles.
func (c *Deployer) checkSecretSchemes(ctx context.Context, env map[string]string) error {
	for k, v := range env {
		for _, m := range domain.EnvRefRe.FindAllStringSubmatch(v, -1) {
			if m[4] == "" {
				continue
			}
			if err := c.mgr.CheckSecretScheme(ctx, m[4]); err != nil {
				return fmt.Errorf("%w: %s: %v", ErrInvalid, k, err)
			}
		}
	}
	return nil
}

// envSources gathers what svc's env resolves against, fetching password
// manager values.
func (c *Deployer) envSources(ctx context.Context, project domain.Project, svc domain.Service) (envSources, error) {
	svcs, err := c.store.ListServices(ctx, svc.ProjectID)
	if err != nil {
		return envSources{}, err
	}
	src := envSources{project: project.Env, dbs: databasesOf(svcs), apps: c.appURLs(svcs), secrets: map[string]string{},
		errors: c.mgr.ErrorsEnv(ctx, project, svc)}
	if refs := secretRefs(refEnv(svc), project.Env); len(refs) > 0 {
		if src.secrets, err = c.mgr.ResolveSecrets(ctx, refs); err != nil {
			return envSources{}, err
		}
	}
	return src, nil
}

// unresolved lists the references in env that src can't resolve, sorted.
func unresolved(env map[string]string, src envSources) []string {
	var out []string
	for _, v := range env {
		for _, m := range domain.EnvRefRe.FindAllStringSubmatch(v, -1) {
			if _, ok := src.lookup(m); !ok {
				out = append(out, domain.RefName(m))
			}
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func checkRefs(env map[string]string, src envSources) error {
	if missing := unresolved(env, src); len(missing) > 0 {
		return fmt.Errorf("%w: unknown reference %s", ErrInvalid, strings.Join(missing, ", "))
	}
	return nil
}

// resolveEnv substitutes references; unknown ones become empty (deploys
// refuse them up front).
func resolveEnv(env map[string]string, src envSources) map[string]string {
	out := make(map[string]string, len(env))
	for k, v := range env {
		out[k] = domain.EnvRefRe.ReplaceAllStringFunc(v, func(m string) string {
			r, _ := src.lookup(domain.EnvRefRe.FindStringSubmatch(m))
			return r
		})
	}
	return out
}

// maskEnv hides the values of secrets; variables stay readable. A secret
// that only references a project entry reveals nothing and stays readable.
func maskEnv(env map[string]string, secrets []string) map[string]string {
	out := make(map[string]string, len(env))
	for k, v := range env {
		if slices.Contains(secrets, k) && !domain.SoleRefRe.MatchString(v) {
			v = identity.SecretMask
		}
		out[k] = v
	}
	return out
}

// managerRef reports whether v is exactly one password manager reference. It
// only says where the secret lives, so it's stored as a readable variable.
func managerRef(v string) bool {
	m := domain.SoleRefRe.FindStringSubmatch(v)
	return m != nil && m[4] != ""
}

// mergeEnv applies an edited env over the stored one. A secret sent back as
// SecretMask keeps its stored value; it can't become a variable without a new
// value, which would reveal it. Password manager references are never secrets.
// secrets nil (a client unaware of them) keeps the stored flags and makes new
// entries secrets.
func mergeEnv(in map[string]string, secrets []string, old map[string]string, oldSecrets []string) (map[string]string, []string, error) {
	if secrets == nil {
		for k := range in {
			if _, had := old[k]; !had || slices.Contains(oldSecrets, k) {
				secrets = append(secrets, k)
			}
		}
	}
	env := make(map[string]string, len(in))
	out := []string{}
	for k, v := range in {
		secret := slices.Contains(secrets, k)
		if prev, had := old[k]; had && v == identity.SecretMask && slices.Contains(oldSecrets, k) {
			if !secret {
				return nil, nil, fmt.Errorf("%w: enter a new value to turn secret %s into a variable", ErrInvalid, k)
			}
			v = prev
		}
		env[k] = v
		if secret && !managerRef(v) {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return env, out, nil
}

func MaskedProject(p domain.Project) domain.Project {
	p.Env = maskEnv(p.Env, p.Secrets)
	if p.Secrets == nil {
		p.Secrets = []string{}
	}
	return p
}

// SetProjectEnv replaces the project's shared variables and secrets (see
// mergeEnv). An entry still referenced by a service cannot be removed.
// Services pick up changes on their next deploy.
func (c *Deployer) SetProjectEnv(ctx context.Context, id string, env map[string]string, secrets []string) (domain.Project, error) {
	p, err := c.store.GetProject(ctx, id)
	if err != nil {
		return domain.Project{}, err
	}
	env, secrets, err = mergeEnv(env, secrets, p.Env, p.Secrets)
	if err != nil {
		return domain.Project{}, err
	}
	if !maps.Equal(env, p.Env) || !slices.Equal(secrets, p.Secrets) {
		if err := identity.Check(ctx, id, identity.PermEnv); err != nil {
			return domain.Project{}, err
		}
		if err := checkSecretRefs(ctx, p.Env, env); err != nil {
			return domain.Project{}, err
		}
	}
	for k := range env {
		if !envKeyRe.MatchString(k) {
			return domain.Project{}, fmt.Errorf("%w: invalid env var name %q", ErrInvalid, k)
		}
	}
	if err := c.checkSecretSchemes(ctx, env); err != nil {
		return domain.Project{}, err
	}
	svcs, err := c.store.ListServices(ctx, id)
	if err != nil {
		return domain.Project{}, err
	}
	src := envSources{project: env, dbs: databasesOf(svcs), apps: c.appURLs(svcs)}
	for _, s := range svcs {
		if missing := unresolved(s.Env, src); len(missing) > 0 {
			return domain.Project{}, fmt.Errorf("%w: %s is used by service %s", ErrInvalid, strings.Join(missing, ", "), s.Name)
		}
	}
	p, err = c.store.SetProjectEnv(ctx, id, env, secrets)
	if err != nil {
		return domain.Project{}, err
	}
	return MaskedProject(p), nil
}
