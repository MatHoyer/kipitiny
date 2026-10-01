package core

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/MatHoyer/kipitiny/internal/store"
)

// envRefRe matches a reference to a project variable in a service env value:
// {{ project.NAME }}, spaces optional.
var envRefRe = regexp.MustCompile(`\{\{\s*project\.([A-Za-z_][A-Za-z0-9_]*)\s*\}\}`)

// envRefs returns the project variables an env references, sorted and unique.
func envRefs(env map[string]string) []string {
	var refs []string
	for _, v := range env {
		for _, m := range envRefRe.FindAllStringSubmatch(v, -1) {
			refs = append(refs, m[1])
		}
	}
	slices.Sort(refs)
	return slices.Compact(refs)
}

// missingRefs lists the variables env references that project lacks.
func missingRefs(env, project map[string]string) []string {
	var missing []string
	for _, r := range envRefs(env) {
		if _, ok := project[r]; !ok {
			missing = append(missing, r)
		}
	}
	return missing
}

func checkRefs(env, project map[string]string) error {
	if missing := missingRefs(env, project); len(missing) > 0 {
		return fmt.Errorf("%w: unknown project variable %s", ErrInvalid, strings.Join(missing, ", "))
	}
	return nil
}

// resolveEnv substitutes project variable references; unknown ones become
// empty (deploys refuse them up front).
func resolveEnv(env, project map[string]string) map[string]string {
	out := make(map[string]string, len(env))
	for k, v := range env {
		out[k] = envRefRe.ReplaceAllStringFunc(v, func(m string) string {
			return project[envRefRe.FindStringSubmatch(m)[1]]
		})
	}
	return out
}

// maskEnv hides env values. A value made only of references holds no secret
// and stays readable.
func maskEnv(env map[string]string) map[string]string {
	out := make(map[string]string, len(env))
	for k, v := range env {
		if v != "" && strings.TrimSpace(envRefRe.ReplaceAllString(v, "")) == "" {
			out[k] = v
		} else {
			out[k] = SecretMask
		}
	}
	return out
}

// mergeMasked replaces values equal to SecretMask with the stored ones.
func mergeMasked(in, stored map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		if old, ok := stored[k]; ok && v == SecretMask {
			v = old
		}
		out[k] = v
	}
	return out
}

func maskedProject(p store.Project) store.Project {
	p.Env = maskEnv(p.Env)
	return p
}

// SetProjectEnv replaces the project's shared variables; values equal to
// SecretMask keep their stored value. A variable still referenced by a
// service cannot be removed. Services pick up changes on their next deploy.
func (c *Core) SetProjectEnv(ctx context.Context, id string, env map[string]string) (store.Project, error) {
	p, err := c.store.GetProject(ctx, id)
	if err != nil {
		return store.Project{}, err
	}
	env = mergeMasked(env, p.Env)
	for k := range env {
		if !envKeyRe.MatchString(k) {
			return store.Project{}, fmt.Errorf("%w: invalid env var name %q", ErrInvalid, k)
		}
	}
	svcs, err := c.store.ListServices(ctx, id)
	if err != nil {
		return store.Project{}, err
	}
	for _, s := range svcs {
		if missing := missingRefs(s.Env, env); len(missing) > 0 {
			return store.Project{}, fmt.Errorf("%w: %s is used by service %s", ErrInvalid, strings.Join(missing, ", "), s.Name)
		}
	}
	p, err = c.store.SetProjectEnv(ctx, id, env)
	if err != nil {
		return store.Project{}, err
	}
	return maskedProject(p), nil
}
