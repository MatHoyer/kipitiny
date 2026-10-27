package core

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/compose"
	"github.com/MatHoyer/kipitiny/internal/config"
	"github.com/MatHoyer/kipitiny/internal/store"
)

// previewParent is a project like most: an app on a domain using a
// database, a worker, a game server and a secret.
func previewParent(t *testing.T, c *Core) store.Project {
	t.Helper()
	ctx := context.Background()
	p, err := c.store.CreateProject(ctx, store.Project{Name: "shop", ServerID: store.LocalServerID,
		Env:     map[string]string{"LOG_LEVEL": "info", "STRIPE_KEY": "sk_live", "SENTRY_DSN": "https://sentry", "MAIL": "{{ pass://Prod/mail/password }}"},
		Secrets: []string{"SENTRY_DSN", "STRIPE_KEY"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []store.Service{
		{Name: "db", Kind: store.ServiceKindPostgres, Image: "postgres:17", Env: map[string]string{pgUser: "u", pgPassword: "prod-pass", pgDatabase: "app"}},
		{Name: "web", Kind: store.ServiceKindApp, Image: "ghcr.io/acme/web:v1", Domain: "shop.example.com", Port: 3000, Replicas: 3,
			Env:     map[string]string{"DATABASE_URL": "{{ db.db.URL }}", "APP_URL": "{{ app.web.URL }}", "API_TOKEN": "t0ken", "STRIPE": "{{ project.STRIPE_KEY }}"},
			Secrets: []string{"API_TOKEN"}},
		{Name: "worker", Kind: store.ServiceKindApp, Image: "ghcr.io/acme/web:v1", Env: map[string]string{"LOG": "{{ project.LOG_LEVEL }}"}},
		{Name: "game", Kind: store.ServiceKindApp, Image: "itzg/minecraft-server", PublishedPorts: []store.PublishedPort{{HostPort: 25565, ContainerPort: 25565, Protocol: "tcp"}}},
	} {
		s.ProjectID, s.ServerID = p.ID, p.ServerID
		if s.Replicas == 0 {
			s.Replicas = 1
		}
		if _, err := c.store.CreateService(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

func TestPreviewFile(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	parent := previewParent(t, c)
	f, vars, _, err := c.previewSource(ctx, parent, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(vars) != 0 {
		t.Errorf("no basic auth: no variable kept, got %v", vars)
	}
	settings := store.PreviewSettings{BaseDomain: "preview.example.com"}
	warnings, err := previewFile(&f, parent, "pr-12", settings, map[string]string{"web": "sha-abc", "worker": "sha256:" + strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	web, worker, game, db := f.Services["web"], f.Services["worker"], f.Services["game"], f.Services["db"]
	switch {
	case f.Name != "shop-pr-12" || len(f.X.Variables) != 0:
		t.Errorf("file: %s %v", f.Name, f.X.Variables)
	case web.X.Domain != "pr-12.preview.example.com" || web.Replicas != 0 || web.Image != "ghcr.io/acme/web:sha-abc":
		t.Errorf("web: %s ×%d %s", web.X.Domain, web.Replicas, web.Image)
	case web.X.Middlewares.Headers["X-Robots-Tag"] != "noindex":
		t.Errorf("web middlewares %+v", web.X.Middlewares)
	case web.Environment["API_TOKEN"] != "{{ project.API_TOKEN }}" || web.Environment["APP_URL"] != "{{ app.web.URL }}":
		t.Errorf("web env: %v", web.Environment)
	case !strings.HasSuffix(worker.Image, "@sha256:"+strings.Repeat("a", 64)):
		t.Errorf("worker: %s", worker.Image)
	case len(game.Ports) != 0 || len(warnings) != 1:
		t.Errorf("game ports %v, warnings %v", game.Ports, warnings)
	case db.X.Password != "":
		t.Errorf("db gets a fresh password, has %q", db.X.Password)
	}
	if _, err := compose.Marshal(f); err != nil {
		t.Fatal(err)
	}

	if _, err := previewFile(&f, parent, "pr-12", settings, map[string]string{"nope": "v2"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("a tag for a missing service: %v", err)
	}
}

func TestPreviewEnv(t *testing.T) {
	c := newTestCore(t, config.Config{})
	parent := previewParent(t, c)
	settings := store.PreviewSettings{
		Env:     map[string]string{"STRIPE_KEY": "sk_test", "API_TOKEN": "preview-token", "LOG_LEVEL": "debug"},
		Secrets: []string{"API_TOKEN", "STRIPE_KEY"},
		Inherit: []string{"SENTRY_DSN"},
	}
	env, secrets := previewEnv(parent, settings, map[string]string{"FEATURE": "on"})
	want := map[string]string{"STRIPE_KEY": "sk_test", "API_TOKEN": "preview-token", "LOG_LEVEL": "debug", "SENTRY_DSN": "https://sentry", "FEATURE": "on"}
	if !maps.Equal(env, want) {
		t.Errorf("env = %v, want %v (no MAIL: it points at production's password manager)", env, want)
	}
	if !slices.Equal(secrets, []string{"API_TOKEN", "SENTRY_DSN", "STRIPE_KEY"}) {
		t.Errorf("secrets = %v", secrets)
	}
}

func TestCheckPreviewEnv(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	parent := previewParent(t, c)
	parentSvcs, _ := c.store.ListServices(ctx, parent.ID)
	f, _, _, err := c.previewSource(ctx, parent, "")
	if err != nil {
		t.Fatal(err)
	}
	settings := store.PreviewSettings{BaseDomain: "preview.example.com"}
	if _, err := previewFile(&f, parent, "pr-1", settings, nil); err != nil {
		t.Fatal(err)
	}

	env, _ := previewEnv(parent, settings, nil)
	err = checkPreviewEnv(f, env, settings, parentSvcs)
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "API_TOKEN, STRIPE_KEY") {
		t.Fatalf("missing secrets are refused, never production's: %v", err)
	}

	settings.Env = map[string]string{"API_TOKEN": "x", "STRIPE_KEY": "sk_test"}
	env, _ = previewEnv(parent, settings, nil)
	if err := checkPreviewEnv(f, env, settings, parentSvcs); err != nil {
		t.Fatalf("all set: %v", err)
	}

	web := f.Services["web"]
	web.Environment["CALLBACK"] = "https://shop.example.com/auth"
	if err := checkPreviewEnv(f, env, settings, parentSvcs); err == nil || !strings.Contains(err.Error(), "web's CALLBACK (shop.example.com)") {
		t.Errorf("a production URL is refused: %v", err)
	}
	web.Environment["CALLBACK"] = "{{ project.PUBLIC }}"
	settings.Env["PUBLIC"] = "https://shop.example.com"
	env, _ = previewEnv(parent, settings, nil)
	if err := checkPreviewEnv(f, env, settings, parentSvcs); err != nil {
		t.Errorf("set in the preview settings on purpose: %v", err)
	}
}

func TestPreviewSettings(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	parent := previewParent(t, c)
	ps, err := c.SetPreviewSettings(ctx, parent.ID, PreviewSettingsInput{BaseDomain: "Preview.Example.com",
		Env: map[string]string{"STRIPE_KEY": "sk_test", "LOG_LEVEL": "debug"}, Secrets: []string{"STRIPE_KEY"}, Inherit: []string{"SENTRY_DSN"}})
	if err != nil {
		t.Fatal(err)
	}
	if ps.BaseDomain != "preview.example.com" || ps.Env["STRIPE_KEY"] != SecretMask || ps.Env["LOG_LEVEL"] != "debug" {
		t.Errorf("saved %+v", ps)
	}
	// The mask sent back keeps the secret.
	if _, err := c.SetPreviewSettings(ctx, parent.ID, PreviewSettingsInput{BaseDomain: "preview.example.com",
		Env: map[string]string{"STRIPE_KEY": SecretMask}, Secrets: []string{"STRIPE_KEY"}}); err != nil {
		t.Fatal(err)
	}
	if stored, _ := c.store.GetPreviewSettings(ctx, parent.ID); stored.Env["STRIPE_KEY"] != "sk_test" || len(stored.Inherit) != 0 {
		t.Errorf("stored %+v", stored)
	}
	for name, in := range map[string]PreviewSettingsInput{
		"bad domain":   {BaseDomain: "not a domain"},
		"not a secret": {Inherit: []string{"LOG_LEVEL"}},
		"short sleep":  {SleepAfterSeconds: 60},
		"negative ttl": {TTLSeconds: -1},
		"bad env name": {Env: map[string]string{"1X": "y"}},
	} {
		if _, err := c.SetPreviewSettings(ctx, parent.ID, in); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := c.DeployPreview(ctx, parent.ID, "Bad_Name", PreviewOptions{}); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad preview name: %v", err)
	}

	// Password manager references need the secret-providers feature.
	useSecretProvider(t, c, &fakeProvider{})("")
	member := WithActor(ctx, Actor{Kind: "user", Name: "alice", Role: store.RoleMember,
		Grants: map[string][]string{parent.ID: {string(PermView), string(PermSettings), string(PermEnv)}}})
	ref := PreviewSettingsInput{Env: map[string]string{"KEY": "{{ fake://prod/key }}"}}
	if _, err := c.SetPreviewSettings(member, parent.ID, ref); !errors.Is(err, ErrForbidden) {
		t.Errorf("member referencing a password manager item: %v", err)
	}
	if _, err := c.SetPreviewSettings(ctx, parent.ID, ref); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SetPreviewSettings(member, parent.ID, ref); err != nil {
		t.Errorf("member keeping a reference: %v", err)
	}
}

func TestPreviewSeedBackups(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	parent := previewParent(t, c)
	svcs, _ := c.store.ListServices(ctx, parent.ID)
	f, _, _, err := c.previewSource(ctx, parent, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.seedBackups(ctx, f, svcs); err == nil || !strings.Contains(err.Error(), "no backup of db") {
		t.Fatalf("no backup: %v", err)
	}
	i := slices.IndexFunc(svcs, func(s store.Service) bool { return s.Name == "db" })
	for _, b := range []store.Backup{
		{Kind: store.BackupKindPostgres, Status: store.OpSucceeded},
		{Kind: store.BackupKindPostgres, Status: store.OpFailed},
	} {
		b.ServiceID, b.ProjectID, b.TargetID = svcs[i].ID, parent.ID, "local"
		if _, err := c.store.CreateBackup(ctx, b); err != nil {
			t.Fatal(err)
		}
	}
	seeds, err := c.seedBackups(ctx, f, svcs)
	if err != nil || len(seeds) != 1 || seeds["db"] == "" {
		t.Fatalf("seeds %v, %v", seeds, err)
	}
	if b, _ := c.store.GetBackup(ctx, seeds["db"]); b.Status != store.OpSucceeded {
		t.Errorf("seeds from a failed backup")
	}
}

func TestPreviewOneReplica(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	parent, err := c.store.CreateProject(ctx, store.Project{Name: "shop", ServerID: store.LocalServerID})
	if err != nil {
		t.Fatal(err)
	}
	pr, err := c.store.CreateProject(ctx, store.Project{Name: "shop-pr-1", ServerID: store.LocalServerID, ParentID: parent.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateService(ctx, pr.ID, ServiceInput{Name: "web", Kind: store.ServiceKindApp, Image: "nginx", Replicas: 2}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("two replicas in a preview: %v", err)
	}
	v, err := c.CreateService(ctx, pr.ID, ServiceInput{Name: "web", Kind: store.ServiceKindApp, Image: "nginx"})
	if err != nil {
		t.Fatal(err)
	}
	two := 2
	if _, err := c.UpdateService(ctx, v.ID, ServicePatch{Replicas: &two}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("scaled a preview's service: %v", err)
	}
	if _, err := c.CreateService(ctx, parent.ID, ServiceInput{Name: "web", Kind: store.ServiceKindApp, Image: "nginx", Replicas: 2}); err != nil {
		t.Fatalf("the parent scales: %v", err)
	}
}

func TestPreviewOnly(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	p, err := c.store.CreateProject(ctx, store.Project{Name: "shop", ServerID: store.LocalServerID})
	if err != nil {
		t.Fatal(err)
	}
	src := []byte(`
services:
  web: {image: a, x-kipitiny: {domain: shop.example.com, port: 80}}
  mailpit: {image: axllent/mailpit, profiles: [preview], x-kipitiny: {domain: mail.example.com, port: 8025}}
  mailer: {image: a, x-kipitiny: {preview: skip}}
`)
	a, err := c.applyCompose(ctx, p.ID, src, ApplyOptions{}, true)
	if err != nil {
		t.Fatal(err)
	}
	svcs, _ := c.store.ListServices(ctx, p.ID)
	if len(svcs) != 2 || slices.ContainsFunc(svcs, func(s store.Service) bool { return s.Name == "mailpit" }) {
		t.Errorf("parent services = %v", svcs)
	}
	if !slices.ContainsFunc(a.plan.Warnings, func(w string) bool { return strings.Contains(w, "mailpit runs in previews only") }) {
		t.Errorf("warnings = %v", a.plan.Warnings)
	}
	// A second sync neither creates nor prunes it.
	if a, err = c.applyCompose(ctx, p.ID, src, ApplyOptions{Prune: true, DryRun: true}, true); err != nil || len(a.plan.Create)+len(a.plan.Delete) > 0 {
		t.Errorf("resync: %v %+v", err, a.plan)
	}

	f, _, err := compose.Parse(src, compose.Vars{})
	if err != nil {
		t.Fatal(err)
	}
	warnings, err := previewFile(&f, p, "pr-1", store.PreviewSettings{BaseDomain: "preview.example.com"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := f.Services["mailer"]; ok || f.Services["mailpit"].X.Preview != "" || f.Services["mailpit"].X.Domain != "pr-1-mailpit.preview.example.com" {
		t.Errorf("preview services = %+v", f.Services)
	}
	if !slices.ContainsFunc(warnings, func(w string) bool { return strings.Contains(w, "mailer left out") }) {
		t.Errorf("warnings = %v", warnings)
	}
}

// A branch's password manager references are left out of the preview: a
// caller with deploy picks the branch.
func TestPreviewSourceBranchRefs(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{DataDir: t.TempDir()})
	parent := previewParent(t, c)
	dir, commit := localRepo(t)
	commit("services:\n  web:\n    image: nginx\n    environment:\n      LEAK: '{{ fake://Prod/billing/apikey }}'\n      PLAIN: plain\n")
	if _, err := c.store.SaveProjectGit(ctx, store.ProjectGit{ProjectID: parent.ID, RepoURL: dir, Branch: "main", Path: "deploy/compose.yaml"}); err != nil {
		t.Fatal(err)
	}
	f, _, _, err := c.previewSource(ctx, parent, "")
	if err != nil {
		t.Fatal(err)
	}
	if env := f.Services["web"].Environment; env["LEAK"] != "{{ project.LEAK }}" || env["PLAIN"] != "plain" {
		t.Errorf("env = %v", env)
	}
}

// A project without git stores where its services run: previews copy the
// stored services.
func TestPreviewOnlyStored(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	p, err := c.store.CreateProject(ctx, store.Project{Name: "shop", ServerID: store.LocalServerID})
	if err != nil {
		t.Fatal(err)
	}
	src := []byte(`
services:
  web: {image: a, x-kipitiny: {domain: shop.example.com, port: 80}}
  mailpit: {image: axllent/mailpit, profiles: [preview], x-kipitiny: {domain: mail.example.com, port: 8025}}
  mailer: {image: a, x-kipitiny: {preview: skip}}
`)
	a, err := c.applyCompose(ctx, p.ID, src, ApplyOptions{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.plan.Create) != 3 {
		t.Fatalf("plan = %+v", a.plan)
	}
	svcs, _ := c.store.ListServices(ctx, p.ID)
	by := map[string]store.Service{}
	for _, s := range svcs {
		by[s.Name] = s
	}
	if by["mailpit"].Preview != "only" || by["mailer"].Preview != "skip" || by["web"].Preview != "" {
		t.Fatalf("stored = %+v", by)
	}
	if !needsDeploy(by["web"], false) || needsDeploy(by["mailpit"], true) {
		t.Error("an apply deploys a previews-only service")
	}
	if _, err := c.Deploy(ctx, by["mailpit"].ID, DeployOptions{}); !errors.Is(err, ErrInvalid) {
		t.Errorf("deployed a previews-only service: %v", err)
	}
	if _, err := c.ServiceAction(ctx, by["mailpit"].ID, ActionStart); !errors.Is(err, ErrInvalid) {
		t.Errorf("started a previews-only service: %v", err)
	}
	// The exported file applies again unchanged.
	f, vars := exportFile(p, svcs, nil, nil, nil, false)
	data, err := compose.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	if a, err = c.applyCompose(ctx, p.ID, data, ApplyOptions{Env: vars, Prune: true, DryRun: true}, false); err != nil ||
		len(a.plan.Create)+len(a.plan.Update)+len(a.plan.Delete) > 0 {
		t.Errorf("reapply: %v %+v", err, a.plan)
	}

	f, _, _, err = c.previewSource(ctx, p, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := previewFile(&f, p, "pr-1", store.PreviewSettings{BaseDomain: "preview.example.com"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.Services["mailer"]; ok || f.Services["mailpit"].X.Preview != "" || f.Services["web"].Image != "a" {
		t.Errorf("preview services = %+v", f.Services)
	}

	bad := "sometimes"
	if _, err := c.UpdateService(ctx, by["web"].ID, ServicePatch{Preview: &bad}); !errors.Is(err, ErrInvalid) {
		t.Errorf("preview %q: %v", bad, err)
	}
	// Switching to only stops the containers: tested on Docker.
	none := ""
	if v, err := c.updateService(ctx, by["mailpit"].ID, ServicePatch{Preview: &none}); err != nil || v.Preview != "" || v.Stopped || !needsDeploy(v, false) {
		t.Errorf("back to both: %v %+v", err, v)
	}

	pr, err := c.store.CreateProject(ctx, store.Project{Name: "shop-pr-1", ServerID: store.LocalServerID, ParentID: p.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateService(ctx, pr.ID, ServiceInput{Name: "web", Image: "nginx", Preview: "skip"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("a preview's service left out of previews: %v", err)
	}
}
