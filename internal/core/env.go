package core

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/MatHoyer/kipitiny/internal/store"
)

// envRefRe matches a reference in a service env value, spaces optional:
// {{ project.NAME }} for a project variable or secret, {{ db.SERVICE.FIELD }}
// for a credential of a postgres service of the project (see dbFields).
var envRefRe = regexp.MustCompile(`\{\{\s*(?:project\.([A-Za-z_][A-Za-z0-9_]*)|db\.([a-z0-9-]+)\.([A-Z]+))\s*\}\}`)

// soleRefRe matches a value that is exactly one reference.
var soleRefRe = regexp.MustCompile(`^\s*` + envRefRe.String() + `\s*$`)

// dbFields are the credentials a database reference can name.
var dbFields = []string{"URL", "HOST", "PORT", "USER", "PASSWORD", "DATABASE"}

// envSources holds what references resolve against.
type envSources struct {
	project map[string]string
	// dbs are the project's postgres services by name.
	dbs map[string]store.Service
}

// projectDatabases returns the project's postgres services by name.
func (c *Core) projectDatabases(ctx context.Context, projectID string) (map[string]store.Service, error) {
	svcs, err := c.store.ListServices(ctx, projectID)
	if err != nil {
		return nil, err
	}
	return databasesOf(svcs), nil
}

func databasesOf(svcs []store.Service) map[string]store.Service {
	dbs := map[string]store.Service{}
	for _, s := range svcs {
		if s.Kind == store.ServiceKindPostgres {
			dbs[s.Name] = s
		}
	}
	return dbs
}

// lookup resolves one envRefRe match (its submatches).
func (src envSources) lookup(m []string) (string, bool) {
	if m[1] != "" {
		v, ok := src.project[m[1]]
		return v, ok
	}
	db, ok := src.dbs[m[2]]
	if !ok {
		return "", false
	}
	switch m[3] {
	case "URL":
		return DatabaseURL(db), true
	case "HOST":
		return db.Name, true
	case "PORT":
		return "5432", true
	case "USER":
		return db.Env[pgUser], true
	case "PASSWORD":
		return db.Env[pgPassword], true
	case "DATABASE":
		return db.Env[pgDatabase], true
	}
	return "", false
}

// refName is how a reference is named in messages: project.NAME, db.SERVICE.FIELD.
func refName(m []string) string {
	if m[1] != "" {
		return "project." + m[1]
	}
	return "db." + m[2] + "." + m[3]
}

// unresolved lists the references in env that src can't resolve, sorted.
func unresolved(env map[string]string, src envSources) []string {
	var out []string
	for _, v := range env {
		for _, m := range envRefRe.FindAllStringSubmatch(v, -1) {
			if _, ok := src.lookup(m); !ok {
				out = append(out, refName(m))
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

// usesDatabase reports whether env references the database named name.
func usesDatabase(env map[string]string, name string) bool {
	for _, v := range env {
		for _, m := range envRefRe.FindAllStringSubmatch(v, -1) {
			if m[2] == name {
				return true
			}
		}
	}
	return false
}

// resolveEnv substitutes references; unknown ones become empty (deploys
// refuse them up front).
func resolveEnv(env map[string]string, src envSources) map[string]string {
	out := make(map[string]string, len(env))
	for k, v := range env {
		out[k] = envRefRe.ReplaceAllStringFunc(v, func(m string) string {
			r, _ := src.lookup(envRefRe.FindStringSubmatch(m))
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
		if slices.Contains(secrets, k) && !soleRefRe.MatchString(v) {
			v = SecretMask
		}
		out[k] = v
	}
	return out
}

// mergeEnv applies an edited env over the stored one. A secret sent back as
// SecretMask keeps its stored value; it can't become a variable without a new
// value, which would reveal it. secrets nil (a client unaware of them) keeps
// the stored flags and makes new entries secrets.
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
		if prev, had := old[k]; had && v == SecretMask && slices.Contains(oldSecrets, k) {
			if !secret {
				return nil, nil, fmt.Errorf("%w: enter a new value to turn secret %s into a variable", ErrInvalid, k)
			}
			v = prev
		}
		env[k] = v
		if secret {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return env, out, nil
}

func maskedProject(p store.Project) store.Project {
	p.Env = maskEnv(p.Env, p.Secrets)
	if p.Secrets == nil {
		p.Secrets = []string{}
	}
	return p
}

// SetProjectEnv replaces the project's shared variables and secrets (see
// mergeEnv). An entry still referenced by a service cannot be removed.
// Services pick up changes on their next deploy.
func (c *Core) SetProjectEnv(ctx context.Context, id string, env map[string]string, secrets []string) (store.Project, error) {
	p, err := c.store.GetProject(ctx, id)
	if err != nil {
		return store.Project{}, err
	}
	env, secrets, err = mergeEnv(env, secrets, p.Env, p.Secrets)
	if err != nil {
		return store.Project{}, err
	}
	for k := range env {
		if !envKeyRe.MatchString(k) {
			return store.Project{}, fmt.Errorf("%w: invalid env var name %q", ErrInvalid, k)
		}
	}
	svcs, err := c.store.ListServices(ctx, id)
	if err != nil {
		return store.Project{}, err
	}
	src := envSources{project: env, dbs: databasesOf(svcs)}
	for _, s := range svcs {
		if missing := unresolved(s.Env, src); len(missing) > 0 {
			return store.Project{}, fmt.Errorf("%w: %s is used by service %s", ErrInvalid, strings.Join(missing, ", "), s.Name)
		}
	}
	p, err = c.store.SetProjectEnv(ctx, id, env, secrets)
	if err != nil {
		return store.Project{}, err
	}
	return maskedProject(p), nil
}
