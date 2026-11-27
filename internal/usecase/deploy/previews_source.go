package deploy

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/kipit-labs/kipitiny/internal/compose"
	"github.com/kipit-labs/kipitiny/internal/domain"
)

// previewSource is the parent as a compose file: its git file at branch
// when it follows git, else exported. secretVars give the ${NAME} values a
// preview may keep (basic auth hashes); branch is the one read.
func (c *Deployer) previewSource(ctx context.Context, parent domain.Project, branch string) (compose.File, map[string]string, string, error) {
	g, err := c.store.GetProjectGit(ctx, parent.ID)
	if err == nil {
		if branch != "" {
			g.Branch = branch
		}
		var f compose.File
		read := false
		err := c.mgr.WithGitFile(ctx, g, func(data []byte, _ string, files func(rel string) ([]domain.ServiceFile, error)) error {
			read = true
			// ${NAME} values the file doesn't get become {{ project.NAME }}:
			// the preview's variables.
			var err error
			f, _, err = compose.Parse(data, compose.Vars{
				Lookup: func(string) (string, bool) { return "", false },
				Ref:    func(_, _, name string) string { return "{{ project." + name + " }}" },
				Files:  files,
			})
			return err
		})
		if !read {
			return compose.File{}, nil, "", fmt.Errorf("read %s at %s: %w", g.Path, g.Branch, err)
		}
		if err != nil {
			return compose.File{}, nil, "", fmt.Errorf("%w: %s at %s: %s", ErrInvalid, g.Path, g.Branch, err.Error())
		}
		// Password manager references are left out, as from an exported
		// file: the branch is the caller's, and its value takes the
		// preview variable of the same name.
		hasRef := func(v string) bool { return len(secretRefs(map[string]string{"": v}, nil)) > 0 }
		vars := map[string]string{}
		for name, s := range f.Services {
			for k, v := range s.Environment {
				if hasRef(v) {
					s.Environment[k] = "{{ project." + k + " }}"
				} else {
					s.Environment[k] = compose.Escape(v)
				}
			}
			// Marshal writes a secret file as the variable giving it: its
			// content, from the repository or a {{ project.NAME }}.
			for i, file := range s.Files {
				if file.Secret {
					v := varName(name, file.Source)
					vars[v], s.Files[i].Content = string(file.Content), []byte(v)
					if hasRef(vars[v]) {
						vars[v] = "{{ project." + nonVarRe.ReplaceAllString(strings.ToUpper(strings.TrimPrefix(file.Source, "secret:")), "_") + " }}"
					}
				} else {
					s.Files[i].Content = []byte(compose.Escape(string(file.Content)))
				}
			}
		}
		return f, vars, g.Branch, nil
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return compose.File{}, nil, "", err
	}
	svcs, err := c.store.ListServices(ctx, parent.ID)
	if err != nil {
		return compose.File{}, nil, "", err
	}
	f, vars := exportFile(parent, svcs, nil, nil, nil, false)
	// A service's secrets become the preview's project variables of the
	// same name; only basic auth hashes stay (previews are protected the
	// same way).
	for name, s := range f.Services {
		for k, v := range s.Environment {
			if v == "${"+varName(name, k)+"}" {
				s.Environment[k] = "{{ project." + k + " }}"
			}
		}
	}
	keep := map[string]string{}
	for k, v := range vars {
		if strings.Contains(k, "_BASIC_AUTH_") {
			keep[k] = v
		}
	}
	// Secret files too: secret NAME is the preview's variable NAME.
	for name, s := range f.Services {
		for _, file := range s.Files {
			if file.Secret {
				keep[varName(name, file.Source)] = "{{ project." + nonVarRe.ReplaceAllString(strings.ToUpper(strings.TrimPrefix(file.Source, "secret:")), "_") + " }}"
			}
		}
	}
	return f, keep, branch, nil
}

// previewSleep is how long a preview stays up unused: its settings' idle
// time, or none when nothing would wake it.
func (c *Deployer) previewSleep(ctx context.Context, p domain.Project, f compose.File, settings domain.PreviewSettings) (int, string) {
	routed := slices.ContainsFunc(slices.Collect(maps.Values(f.Services)), func(s compose.Service) bool { return s.X.Domain != "" })
	if !routed {
		return 0, ""
	}
	if err := c.checkSleepServer(ctx, p); err != nil {
		return 0, "the preview doesn't sleep: on a remote server, sleep mode needs the manager's domain (KIPITINY_DOMAIN)"
	}
	if settings.SleepAfterSeconds != 0 {
		return settings.SleepAfterSeconds, ""
	}
	return int(defaultPreviewSleep.Seconds()), ""
}

// previewFile turns the parent's file into the preview's: apps on the base
// domain, asleep when unused, no published ports, host access, uptime
// checks or several replicas, fresh databases, and CI's images. It returns
// what was left out.
func previewFile(f *compose.File, parent domain.Project, name string, settings domain.PreviewSettings, tags map[string]string) ([]string, error) {
	f.Name, f.X = parent.Name+"-"+name, compose.FileExt{}
	var warnings []string
	// Services sharing a domain share the preview's: the one named first
	// gives it its label.
	owner := map[string]string{}
	for _, svcName := range f.ServiceNames() {
		s := f.Services[svcName]
		if _, ok := owner[s.X.Domain]; !ok && s.X.Domain != "" && s.X.Port > 0 && s.X.Preview != "skip" {
			owner[s.X.Domain] = svcName
		}
	}
	routed := len(owner)
	for _, svcName := range f.ServiceNames() {
		s := f.Services[svcName]
		if s.HostNetwork || s.DockerSocket != "" || (s.Runtime != nil && s.Runtime.HostAccess()) ||
			slices.ContainsFunc(s.Volumes, domain.Volume.HostAccess) {
			warnings = append(warnings, fmt.Sprintf("%s left out: previews don't get host access", svcName))
			delete(f.Services, svcName)
			continue
		}
		if s.X.Preview == "skip" {
			warnings = append(warnings, fmt.Sprintf("%s left out: %s.preview is skip", svcName, compose.ExtKey))
			delete(f.Services, svcName)
			continue
		}
		s.X.Preview = "" // the preview's own service
		if len(s.Ports) > 0 {
			warnings = append(warnings, fmt.Sprintf("%s: published ports left out (the parent holds them)", svcName))
			s.Ports = nil
		}
		s.Replicas = 0
		s.X.Uptime = nil
		s.X.Analytics = false // prod's numbers only
		s.X.Anonymize = nil   // only the parent's databases are anonymized
		s.X.Password = ""     // a fresh database
		if s.X.Port == 0 {
			s.X.Domain = ""
		}
		if s.X.Domain != "" {
			label := name + "-" + cmp.Or(owner[s.X.Domain], svcName)
			if routed == 1 {
				label = name
			}
			if len(label) > maxLabel {
				return nil, fmt.Errorf("%w: %s.%s is too long for a hostname", ErrInvalid, label, settings.BaseDomain)
			}
			s.X.Domain = label + "." + settings.BaseDomain
			m := s.X.Middlewares
			if m == nil {
				m = &compose.Middlewares{}
			}
			m.Headers = maps.Clone(m.Headers)
			if m.Headers == nil {
				m.Headers = map[string]string{}
			}
			m.Headers["X-Robots-Tag"] = "noindex"
			s.X.Middlewares = m
		}
		if tag, ok := tags[svcName]; ok {
			t, d := tag, ""
			if strings.HasPrefix(tag, "sha256:") {
				t, d = "", tag
			}
			img, err := retagImage(s.Image, t, d)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", svcName, err)
			}
			s.Image = img
		}
		f.Services[svcName] = s
	}
	for svcName := range tags {
		if _, ok := f.Services[svcName]; !ok {
			return nil, fmt.Errorf("%w: no service %q to give a tag to", ErrInvalid, svcName)
		}
	}
	return warnings, nil
}

// previewEnv is a preview's project variables: the parent's plain
// variables, the secrets it lets previews inherit, then the preview
// settings' and CI's values. A parent value pointing at a password manager
// counts as a secret.
func previewEnv(parent domain.Project, settings domain.PreviewSettings, ci map[string]string) (map[string]string, []string) {
	env := map[string]string{}
	var secrets []string
	for k, v := range parent.Env {
		switch {
		case slices.Contains(settings.Inherit, k):
			env[k] = v
			if slices.Contains(parent.Secrets, k) {
				secrets = append(secrets, k)
			}
		case !slices.Contains(parent.Secrets, k) && len(secretRefs(map[string]string{k: v}, nil)) == 0:
			env[k] = v
		}
	}
	for k, v := range settings.Env {
		env[k] = v
		secrets = slices.DeleteFunc(secrets, func(s string) bool { return s == k })
		if slices.Contains(settings.Secrets, k) {
			secrets = append(secrets, k)
		}
	}
	for k, v := range ci {
		env[k] = v
		secrets = slices.DeleteFunc(secrets, func(s string) bool { return s == k })
	}
	slices.Sort(secrets)
	return env, secrets
}

// checkPreviewEnv refuses a preview that would miss a value (rather than
// fall back to production's) or point at production: a value naming one of
// the parent's domains, unless the preview settings set it on purpose.
func checkPreviewEnv(f compose.File, env map[string]string, settings domain.PreviewSettings, parentSvcs []domain.Service) error {
	var domains []string
	for _, s := range parentSvcs {
		if s.Domain != "" {
			domains = append(domains, s.Domain)
		}
	}
	src := envSources{project: env}
	var missing, prod []string
	for _, name := range f.ServiceNames() {
		s := f.Services[name]
		for _, m := range unresolved(s.Environment, src) {
			if strings.HasPrefix(m, "project.") {
				missing = append(missing, strings.TrimPrefix(m, "project."))
			}
		}
		for _, k := range slices.Sorted(maps.Keys(s.Environment)) {
			v := s.Environment[k]
			var via []string // the project variables v uses
			for _, m := range domain.EnvRefRe.FindAllStringSubmatch(v, -1) {
				if m[1] != "" {
					via = append(via, m[1])
				}
			}
			resolved := resolveEnv(map[string]string{k: v}, src)[k]
			for _, d := range domains {
				if !strings.Contains(resolved, d) {
					continue
				}
				if slices.ContainsFunc(via, func(n string) bool { _, set := settings.Env[n]; return set && strings.Contains(settings.Env[n], d) }) {
					continue
				}
				prod = append(prod, fmt.Sprintf("%s's %s (%s)", name, k, d))
			}
		}
	}
	slices.Sort(missing)
	missing = slices.Compact(missing)
	switch {
	case len(missing) > 0:
		return fmt.Errorf("%w: previews need a value for %s: set it in the project's preview settings, or let previews inherit the project's secret",
			ErrInvalid, strings.Join(missing, ", "))
	case len(prod) > 0:
		return fmt.Errorf("%w: %s would point a preview at production: use {{ app.<service>.URL }}, or set the variable in the preview settings",
			ErrInvalid, strings.Join(prod, ", "))
	}
	return nil
}
