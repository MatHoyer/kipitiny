package core

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/MatHoyer/kipitiny/internal/compose"
	"github.com/MatHoyer/kipitiny/internal/docker"
	"github.com/MatHoyer/kipitiny/internal/store"
)

const (
	// defaultPreviewTTL closes a preview nobody used or deployed for a week.
	defaultPreviewTTL = 7 * 24 * time.Hour
	// defaultPreviewSleep is when a preview's apps sleep, unless its
	// project's settings say otherwise.
	defaultPreviewSleep = 30 * time.Minute
	previewExpiryEvery  = time.Hour
	// maxLabel is a DNS label's length.
	maxLabel = 63
)

// PreviewSettingsInput sets how a project's previews differ from it; see
// store.PreviewSettings. Env entries sent back as SecretMask keep their
// value.
type PreviewSettingsInput struct {
	BaseDomain        string            `json:"baseDomain"`
	Env               map[string]string `json:"env"`
	Secrets           []string          `json:"secrets"`
	Inherit           []string          `json:"inherit"`
	TTLSeconds        int               `json:"ttlSeconds"`
	SleepAfterSeconds int               `json:"sleepAfterSeconds"`
	// Seed is what --seed fills a new preview's databases with.
	Seed store.SeedMode `json:"seed"`
}

// GetPreviewSettings returns a project's preview settings, secrets masked;
// a project without any gets empty ones.
func (c *Core) GetPreviewSettings(ctx context.Context, projectID string) (store.PreviewSettings, error) {
	if _, err := c.store.GetProject(ctx, projectID); err != nil {
		return store.PreviewSettings{}, err
	}
	ps, err := c.previewSettings(ctx, projectID)
	ps.Env = maskEnv(ps.Env, ps.Secrets)
	return ps, err
}

func (c *Core) previewSettings(ctx context.Context, projectID string) (store.PreviewSettings, error) {
	ps, err := c.store.GetPreviewSettings(ctx, projectID)
	if errors.Is(err, store.ErrNotFound) {
		return store.PreviewSettings{ProjectID: projectID, Env: map[string]string{}, Secrets: []string{}, Inherit: []string{}}, nil
	}
	return ps, err
}

// SetPreviewSettings replaces a project's preview settings. They apply to
// each preview on its next deploy.
func (c *Core) SetPreviewSettings(ctx context.Context, projectID string, in PreviewSettingsInput) (store.PreviewSettings, error) {
	p, err := c.store.GetProject(ctx, projectID)
	if err != nil {
		return store.PreviewSettings{}, err
	}
	if p.ParentID != "" {
		return store.PreviewSettings{}, fmt.Errorf("%w: a preview has no previews of its own", ErrInvalid)
	}
	old, err := c.previewSettings(ctx, projectID)
	if err != nil {
		return store.PreviewSettings{}, err
	}
	ps := store.PreviewSettings{
		ProjectID:         projectID,
		BaseDomain:        strings.ToLower(strings.TrimSpace(in.BaseDomain)),
		Inherit:           []string{},
		TTLSeconds:        in.TTLSeconds,
		SleepAfterSeconds: in.SleepAfterSeconds,
		SeedMode:          in.Seed,
		Salt:              old.Salt,
	}
	if ps.BaseDomain != "" && !domainRe.MatchString(ps.BaseDomain) {
		return store.PreviewSettings{}, fmt.Errorf("%w: base domain %q is not a valid hostname", ErrInvalid, ps.BaseDomain)
	}
	if !slices.Contains([]store.SeedMode{store.SeedOff, store.SeedAnonymized, store.SeedRaw}, ps.SeedMode) {
		return store.PreviewSettings{}, fmt.Errorf("%w: seed is empty (off), anonymized or raw", ErrInvalid)
	}
	if ps.TTLSeconds < 0 {
		return store.PreviewSettings{}, fmt.Errorf("%w: the time to live can't be negative", ErrInvalid)
	}
	if s := ps.SleepAfterSeconds; s != 0 && (s < minSleepAfterSeconds || s > maxSleepAfterSeconds) {
		return store.PreviewSettings{}, fmt.Errorf("%w: sleep after must be between %d minutes and %d days", ErrInvalid, minSleepAfterSeconds/60, maxSleepAfterSeconds/86400)
	}
	if in.Env == nil {
		in.Env = map[string]string{}
	}
	for k := range in.Env {
		if !envKeyRe.MatchString(k) {
			return store.PreviewSettings{}, fmt.Errorf("%w: invalid env var name %q", ErrInvalid, k)
		}
	}
	if ps.Env, ps.Secrets, err = mergeEnv(in.Env, in.Secrets, old.Env, old.Secrets); err != nil {
		return store.PreviewSettings{}, err
	}
	if err := checkSecretRefs(ctx, old.Env, ps.Env); err != nil {
		return store.PreviewSettings{}, err
	}
	if err := c.checkSecretSchemes(ctx, ps.Env); err != nil {
		return store.PreviewSettings{}, err
	}
	for _, k := range in.Inherit {
		if !slices.Contains(p.Secrets, k) {
			return store.PreviewSettings{}, fmt.Errorf("%w: %s is not a secret of the project", ErrInvalid, k)
		}
		ps.Inherit = append(ps.Inherit, k)
	}
	slices.Sort(ps.Inherit)
	ps, err = c.store.SavePreviewSettings(ctx, ps)
	ps.Env = maskEnv(ps.Env, ps.Secrets)
	return ps, err
}

// PreviewOptions are what CI gives for a preview's deploy.
type PreviewOptions struct {
	// Tags are the image tags (or sha256: digests) to deploy, by service;
	// the others keep the parent's image.
	Tags map[string]string `json:"tags"`
	// Branch is the source branch: a git-linked parent's compose file is
	// read there (default: the branch it follows).
	Branch string `json:"branch"`
	Commit string `json:"commit"`
	// Env are project variables for this preview only (not secrets).
	Env map[string]string `json:"env"`
	// TTLSeconds overrides the settings' time to live; 0 keeps it.
	TTLSeconds int `json:"ttlSeconds"`
	// Seed fills the databases a deploy creates with the latest backup of
	// the parent's database of the same name, before the apps deploy. The
	// preview settings must allow it: it copies production's data.
	Seed bool `json:"seed"`
}

// PreviewView is a preview with where its apps are.
type PreviewView struct {
	store.Project
	// URLs of its public apps, by service name.
	URLs map[string]string `json:"urls"`
	// Plan is what the last deploy changed (DeployPreview only).
	Plan *ComposePlan `json:"plan,omitempty"`
}

// ListPreviews returns a project's previews.
func (c *Core) ListPreviews(ctx context.Context, parentID string) ([]PreviewView, error) {
	ps, err := c.store.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	out := []PreviewView{}
	for _, p := range ps {
		if p.ParentID != parentID || parentID == "" {
			continue
		}
		v, err := c.previewView(ctx, p)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func (c *Core) previewView(ctx context.Context, p store.Project) (PreviewView, error) {
	svcs, err := c.store.ListServices(ctx, p.ID)
	if err != nil {
		return PreviewView{}, err
	}
	return PreviewView{Project: maskedProject(p), URLs: c.appURLs(svcs)}, nil
}

// findPreview returns the parent's preview named name.
func (c *Core) findPreview(ctx context.Context, parentID, name string) (store.Project, error) {
	ps, err := c.store.ListProjects(ctx)
	if err != nil {
		return store.Project{}, err
	}
	for _, p := range ps {
		if p.ParentID == parentID && p.Preview.Name == name {
			return p, nil
		}
	}
	return store.Project{}, fmt.Errorf("%w: preview %q", store.ErrNotFound, name)
}

// withoutActor runs work the caller was authorized for on the parent as the
// system: a preview is a project of its own, which the actor has no grant on
// until it exists.
func withoutActor(ctx context.Context) context.Context {
	return context.WithValue(ctx, actorKey{}, nil)
}

// DeployPreview creates or updates the parent's preview named name: a copy
// of the parent (its compose file at opts.Branch when it follows git) with
// fresh databases, its apps on the preview base domain and asleep when
// unused, the project variables of the preview settings instead of the
// parent's secrets, and the images CI built. Services that change deploy in
// the background.
func (c *Core) DeployPreview(ctx context.Context, parentID, name string, opts PreviewOptions) (PreviewView, error) {
	parent, err := c.store.GetProject(ctx, parentID)
	if err != nil {
		return PreviewView{}, err
	}
	if parent.ParentID != "" {
		return PreviewView{}, fmt.Errorf("%w: a preview has no previews of its own", ErrInvalid)
	}
	projectName := parent.Name + "-" + name
	if !nameRe.MatchString(name) || !nameRe.MatchString(projectName) {
		return PreviewView{}, fmt.Errorf("%w: the preview name must be lowercase letters, digits and dashes, and fit in %s-<name> (max 40)", ErrInvalid, parent.Name)
	}
	for k, v := range opts.Env {
		if !envKeyRe.MatchString(k) {
			return PreviewView{}, fmt.Errorf("%w: invalid env var name %q", ErrInvalid, k)
		}
		if envRefRe.MatchString(v) {
			// CI with the deploy permission must not reach secrets through a reference.
			return PreviewView{}, fmt.Errorf("%w: %s: CI's values are plain text, references go in the preview settings", ErrInvalid, k)
		}
	}
	settings, err := c.previewSettings(ctx, parentID)
	if err != nil {
		return PreviewView{}, err
	}
	if settings.BaseDomain == "" {
		return PreviewView{}, fmt.Errorf("%w: set the base domain of %s's previews first (project settings › Previews)", ErrInvalid, parent.Name)
	}
	unlock, err := c.lockService("preview:" + parentID + "/" + name)
	if err != nil {
		return PreviewView{}, err
	}
	defer unlock()
	ctx = withoutActor(ctx)

	file, secretVars, branch, err := c.previewSource(ctx, parent, opts.Branch)
	if err != nil {
		return PreviewView{}, err
	}
	var warnings []string
	if warnings, err = previewFile(&file, parent, name, settings, opts.Tags); err != nil {
		return PreviewView{}, err
	}
	env, secrets := previewEnv(parent, settings, opts.Env)
	parentSvcs, err := c.store.ListServices(ctx, parentID)
	if err != nil {
		return PreviewView{}, err
	}
	if err := checkPreviewEnv(file, env, settings, parentSvcs); err != nil {
		return PreviewView{}, err
	}
	var seeds map[string]string // database name -> parent's backup ID
	if opts.Seed {
		if settings.SeedMode == store.SeedOff {
			return PreviewView{}, fmt.Errorf("%w: %s's preview settings don't allow seeding previews with its data", ErrInvalid, parent.Name)
		}
		if settings.SeedMode == store.SeedAnonymized {
			// shortcut: anonymized copies aren't made yet; never fall back to raw data.
			return PreviewView{}, fmt.Errorf("%w: anonymized seeding isn't available yet", ErrInvalid)
		}
		if seeds, err = c.seedBackups(ctx, file, parentSvcs); err != nil {
			return PreviewView{}, err
		}
	}
	data, err := compose.Marshal(file)
	if err != nil {
		return PreviewView{}, err
	}

	p, err := c.findPreview(ctx, parentID, name)
	if errors.Is(err, store.ErrNotFound) {
		p, err = c.store.CreateProject(ctx, store.Project{Name: projectName, ServerID: parent.ServerID, ParentID: parentID, Preview: store.Preview{Name: name}})
		if err == nil {
			err = c.dockerFor(p.ServerID).EnsureNetwork(ctx, docker.ProjectNetwork(p.ID))
		}
	}
	if err != nil {
		return PreviewView{}, err
	}
	if p, err = c.store.SetProjectEnv(ctx, p.ID, env, secrets); err != nil {
		return PreviewView{}, err
	}
	// Previews show the parent's pages; maintenance mode stays their own.
	m := parent.Maintenance
	m.Enabled = p.Maintenance.Enabled
	if err := c.store.SetProjectMaintenance(ctx, p.ID, m); err != nil {
		return PreviewView{}, err
	}
	p.Maintenance = m
	if sleep, warning := c.previewSleep(ctx, p, file, settings); sleep != p.SleepAfterSeconds {
		if err := c.store.SetProjectSleep(ctx, p.ID, sleep); err != nil {
			return PreviewView{}, err
		}
		p.SleepAfterSeconds = sleep
		if warning != "" {
			warnings = append(warnings, warning)
		}
	}
	apply := ApplyOptions{Env: secretVars, Prune: true, Deploy: true, Commit: opts.Commit}
	if len(seeds) > 0 {
		apply.afterDatabases = func(ctx context.Context, created map[string]string) { c.seedPreview(ctx, p, seeds, created) }
	}
	a, err := c.applyCompose(ctx, p.ID, data, apply, false)
	if a == nil {
		return PreviewView{}, err
	}
	a.plan.Warnings = append(a.plan.Warnings, warnings...)
	if err != nil {
		return PreviewView{Project: maskedProject(p), Plan: &a.plan}, err
	}
	ttl := opts.TTLSeconds
	if ttl == 0 {
		ttl = settings.TTLSeconds
	}
	if ttl == 0 {
		ttl = int(defaultPreviewTTL.Seconds())
	}
	p.Preview = store.Preview{Name: name, Branch: branch, Commit: opts.Commit, TTLSeconds: ttl, DeployedAt: time.Now().UTC()}
	if err := c.store.SetProjectPreview(ctx, p.ID, p.Preview); err != nil {
		return PreviewView{}, err
	}
	v, err := c.previewView(ctx, p)
	v.Plan = &a.plan
	return v, err
}

// seedBackups picks the latest successful backup of each parent database
// the preview has, refusing when one has none.
func (c *Core) seedBackups(ctx context.Context, f compose.File, parentSvcs []store.Service) (map[string]string, error) {
	out := map[string]string{}
	var none []string
	for _, s := range parentSvcs {
		if _, listed := f.Services[s.Name]; !listed || !s.Kind.IsDatabase() {
			continue
		}
		bs, err := c.store.ListBackups(ctx, store.BackupFilter{ServiceID: s.ID, Status: store.OpSucceeded, Limit: 20})
		if err != nil {
			return nil, err
		}
		i := slices.IndexFunc(bs, func(b store.Backup) bool { return b.Kind == store.BackupKindPostgres || b.Kind == store.BackupKindDump })
		if i < 0 {
			none = append(none, s.Name)
			continue
		}
		out[s.Name] = bs[i].ID
	}
	if len(none) > 0 {
		return nil, fmt.Errorf("%w: no backup of %s to seed the preview with", ErrInvalid, strings.Join(none, ", "))
	}
	return out, nil
}

// seedPreview restores the parent's backups into the databases this
// deploy created (a later deploy keeps the preview's data), waiting for
// each before the apps deploy.
func (c *Core) seedPreview(ctx context.Context, p store.Project, seeds, created map[string]string) {
	for name, backupID := range seeds {
		id, ok := created[name]
		if !ok {
			continue
		}
		r, err := c.RestoreBackup(ctx, backupID, id, name)
		if err == nil {
			err = c.waitRestore(ctx, id, r.ID)
		}
		c.Audit(ctx, "preview.seed", p.Name+"/"+name, 0, err)
		if err != nil {
			c.log.Warn("seed preview", "preview", p.Name, "database", name, "err", err)
		}
	}
}

// waitRestore waits for a restore of service id to finish.
func (c *Core) waitRestore(ctx context.Context, id, restoreID string) error {
	deadline := time.Now().Add(restoreTimeout)
	for time.Now().Before(deadline) {
		rs, err := c.store.ListRestores(ctx, id, 5)
		if err != nil {
			return err
		}
		for _, r := range rs {
			if r.ID != restoreID || r.Status == store.OpRunning {
				continue
			}
			if r.Status != store.OpSucceeded {
				return errors.New(r.Error)
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return errors.New("the restore is still running")
}

// previewSource is the parent as a compose file: its git file at branch
// when it follows git, else exported. secretVars give the ${NAME} values a
// preview may keep (basic auth hashes); branch is the one read.
func (c *Core) previewSource(ctx context.Context, parent store.Project, branch string) (compose.File, map[string]string, string, error) {
	g, err := c.store.GetProjectGit(ctx, parent.ID)
	if err == nil {
		if branch != "" {
			g.Branch = branch
		}
		var f compose.File
		read := false
		err := c.withGitFile(ctx, g, func(data []byte, _ string, files repoFiles) error {
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
			return compose.File{}, nil, "", fmt.Errorf("%w: %s at %s: %v", ErrInvalid, g.Path, g.Branch, err)
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
	if !errors.Is(err, store.ErrNotFound) {
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
func (c *Core) previewSleep(ctx context.Context, p store.Project, f compose.File, settings store.PreviewSettings) (int, string) {
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
func previewFile(f *compose.File, parent store.Project, name string, settings store.PreviewSettings, tags map[string]string) ([]string, error) {
	f.Name, f.X = parent.Name+"-"+name, compose.FileExt{}
	var warnings []string
	routed := 0
	for _, s := range f.Services {
		if s.X.Domain != "" && s.X.Port > 0 && s.X.Preview != "skip" {
			routed++
		}
	}
	for _, svcName := range f.ServiceNames() {
		s := f.Services[svcName]
		if s.HostNetwork || s.DockerSocket != "" || (s.Runtime != nil && s.Runtime.HostAccess()) ||
			slices.ContainsFunc(s.Volumes, store.Volume.HostAccess) {
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
			label := name + "-" + svcName
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
func previewEnv(parent store.Project, settings store.PreviewSettings, ci map[string]string) (map[string]string, []string) {
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
func checkPreviewEnv(f compose.File, env map[string]string, settings store.PreviewSettings, parentSvcs []store.Service) error {
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
			for _, m := range envRefRe.FindAllStringSubmatch(v, -1) {
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

// ClosePreview deletes the parent's preview named name: containers,
// volumes, databases and records.
func (c *Core) ClosePreview(ctx context.Context, parentID, name string) error {
	p, err := c.findPreview(ctx, parentID, name)
	if err != nil {
		return err
	}
	return c.deleteProject(withoutActor(ctx), p)
}

// StartPreviewExpiry closes previews nobody requested or deployed for their
// time to live.
func (c *Core) StartPreviewExpiry() error {
	return c.goLoop(func() {
		tick := time.NewTicker(previewExpiryEvery)
		defer tick.Stop()
		for {
			select {
			case <-c.bg.Done():
				return
			case <-tick.C:
			}
			c.expirePreviews(c.bg)
		}
	})
}

func (c *Core) expirePreviews(ctx context.Context) {
	ps, err := c.store.ListProjects(ctx)
	if err != nil {
		return
	}
	now := time.Now()
	for _, p := range ps {
		if p.ParentID == "" || p.Preview.TTLSeconds == 0 {
			continue
		}
		last := p.Preview.DeployedAt
		c.sleep.mu.Lock()
		if t := c.sleep.lastActive[p.ID]; t.After(last) {
			last = t
		}
		c.sleep.mu.Unlock()
		if now.Sub(last) < time.Duration(p.Preview.TTLSeconds)*time.Second {
			continue
		}
		err := c.deleteProject(ctx, p)
		c.Audit(ctx, "preview.expired", p.Name, 0, err)
		if err != nil {
			c.log.Warn("close expired preview", "preview", p.Name, "err", err)
			continue
		}
		c.log.Info("closed unused preview", "preview", p.Name, "idle", now.Sub(last).Round(time.Minute))
	}
}
