package core

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/MatHoyer/kipitiny/internal/config"
	"strings"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/compose"
	"github.com/MatHoyer/kipitiny/internal/store"
)

func TestExportFile(t *testing.T) {
	p := store.Project{Name: "shop", Env: map[string]string{"REGION": "eu", "TOKEN": "t0k"}, Secrets: []string{"TOKEN"}}
	svcs := []store.Service{
		{
			ID: "w1", Name: "web", Kind: store.ServiceKindApp, Image: "ghcr.io/me/shop:1", Replicas: 2, Port: 3000, Domain: "shop.example.com",
			Env:     map[string]string{"API_KEY": "s3cr$t", "MODE": "a$b", "DATABASE_URL": "{{ db.db.URL }}", "SHARED": "{{ project.TOKEN }}"},
			Secrets: []string{"API_KEY", "SHARED"},
			Volumes: []store.Volume{{Name: "data", Path: "/data"}},
			Middlewares: store.Middlewares{BasicAuth: []store.BasicAuthUser{
				{Name: "admin", Hash: "$2a$10$x"}, {Name: "ops", Ref: "{{ pass://V/I/p }}"},
			}},
		},
		{Name: "worker", Kind: store.ServiceKindApp, Image: "ghcr.io/me/worker:1", Replicas: 1, Volumes: []store.Volume{{Name: "data", Path: "/d"}}},
		{Name: "db", Kind: store.ServiceKindPostgres, Image: "postgres:17-alpine", Replicas: 1, MemoryMB: 512,
			Env: map[string]string{pgUser: "app", pgPassword: "pw", pgDatabase: "app"}, Secrets: []string{pgPassword}},
	}
	checks := map[string]store.UptimeCheck{"w1": {ServiceID: "w1", Path: "/health", IntervalSec: 60, TimeoutSec: 10, Enabled: true}}
	f, env := exportFile(p, svcs, checks, true)
	web := f.Services["web"]
	if u := web.X.Uptime; u == nil || *u != (compose.Uptime{Path: "/health"}) {
		t.Errorf("uptime = %+v", u)
	}
	if f.Services["worker"].X.Uptime != nil {
		t.Error("worker has no check")
	}
	if web.Environment["API_KEY"] != "${WEB_API_KEY}" || env["WEB_API_KEY"] != "s3cr$t" {
		t.Errorf("secret: %q, env %q", web.Environment["API_KEY"], env)
	}
	if web.Environment["MODE"] != "a$$b" || web.Environment["SHARED"] != "{{ project.TOKEN }}" || web.Environment["DATABASE_URL"] != "{{ db.db.URL }}" {
		t.Errorf("environment = %q", web.Environment)
	}
	if web.Volumes[0].Name != "web-data" || f.Services["worker"].Volumes[0].Name != "worker-data" {
		t.Errorf("shared volume names not prefixed: %+v", web.Volumes)
	}
	if u := web.X.Middlewares.BasicAuth; u[0].Hash != "${WEB_BASIC_AUTH_ADMIN}" || env["WEB_BASIC_AUTH_ADMIN"] != "$2a$10$x" || u[1].Ref == "" || u[1].Hash != "" {
		t.Errorf("basic auth = %+v", u)
	}
	db := f.Services["db"]
	if db.X.Kind != "postgres" || db.Environment != nil || db.X.Password != "${DB_PASSWORD}" || env["DB_PASSWORD"] != "pw" {
		t.Errorf("db = %+v", db)
	}
	if f.X.Variables["TOKEN"] != "${PROJECT_TOKEN}" || f.X.Variables["REGION"] != "eu" || env["PROJECT_TOKEN"] != "t0k" {
		t.Errorf("project variables = %q", f.X.Variables)
	}

	// What it writes parses back, with the .env giving the secrets.
	data, err := compose.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	back, warns, err := compose.Parse(data, compose.Vars{Lookup: func(n string) (string, bool) { v, ok := env[n]; return v, ok }})
	if err != nil || len(warns) > 0 {
		t.Fatalf("parse: %v %v\n%s", err, warns, data)
	}
	if got := back.Services["web"].Environment; got["API_KEY"] != "s3cr$t" || got["MODE"] != "a$b" {
		t.Errorf("parsed environment = %q", got)
	}
	if strings.Contains(string(data), "s3cr") || strings.Contains(string(data), "t0k") {
		t.Errorf("secret value in compose:\n%s", data)
	}
}

func TestApplyCompose(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	p, err := c.store.CreateProject(ctx, store.Project{Name: "shop", ServerID: store.LocalServerID})
	if err != nil {
		t.Fatal(err)
	}
	src := `
x-kipitiny:
  variables: {REGION: eu, TOKEN: "${PROJECT_TOKEN}"}
  secrets: [TOKEN]
services:
  db:
    image: postgres:17-alpine
    x-kipitiny: {password: "${DB_PASSWORD}"}
  web:
    image: ghcr.io/me/shop:1
    environment:
      DATABASE_URL: "{{ db.db.URL }}"
      API_KEY: ${WEB_API_KEY}
      REGION: "{{ project.REGION }}"
    x-kipitiny:
      domain: shop.example.com
      port: 3000
      secrets: [API_KEY]
      uptime: {path: /health, interval: 2m}
      middlewares:
        basic_auth: [{name: admin, hash: "${WEB_BASIC_AUTH_ADMIN}"}]
`
	hash := "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"
	env := map[string]string{"PROJECT_TOKEN": "t0k", "DB_PASSWORD": "dbpw", "WEB_API_KEY": "k3y", "WEB_BASIC_AUTH_ADMIN": hash}

	plan, err := c.ApplyCompose(ctx, p.ID, []byte(src), ApplyOptions{Env: env, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(plan.Create, []string{"db", "web"}) || !slices.Equal(plan.Variables, []string{"REGION", "TOKEN"}) {
		t.Fatalf("dry run plan = %+v", plan)
	}
	if svcs, _ := c.store.ListServices(ctx, p.ID); len(svcs) != 0 {
		t.Fatal("dry run created services")
	}

	if _, err := c.ApplyCompose(ctx, p.ID, []byte(src), ApplyOptions{Env: env}); err != nil {
		t.Fatal(err)
	}
	svcs, _ := c.store.ListServices(ctx, p.ID)
	byName := map[string]store.Service{}
	for _, s := range svcs {
		byName[s.Name] = s
	}
	if db := byName["db"]; db.Kind != store.ServiceKindPostgres || db.Env[pgPassword] != "dbpw" {
		t.Errorf("db = %+v", db)
	}
	web := byName["web"]
	if web.Env["API_KEY"] != "k3y" || !slices.Equal(web.Secrets, []string{"API_KEY"}) || web.Middlewares.BasicAuth[0].Hash != hash {
		t.Errorf("web = %+v", web)
	}
	if chk, err := c.store.GetUptimeCheck(ctx, web.ID); err != nil || chk.Path != "/health" || chk.IntervalSec != 120 ||
		chk.TimeoutSec != defaultUptimeTimeout || !chk.Enabled {
		t.Errorf("uptime check = %+v, %v", chk, err)
	}
	project, _ := c.store.GetProject(ctx, p.ID)
	if project.Env["TOKEN"] != "t0k" || !slices.Equal(project.Secrets, []string{"TOKEN"}) {
		t.Errorf("project = %+v", project)
	}

	// Its own export, without the .env, applies as unchanged: secrets
	// without values keep theirs.
	out, err := c.ExportCompose(ctx, p.ID, ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	plan, err = c.ApplyCompose(ctx, p.ID, out.Compose, ApplyOptions{DryRun: true})
	if err != nil {
		t.Fatalf("%v\n%s", err, out.Compose)
	}
	if len(plan.Create)+len(plan.Update)+len(plan.Variables) != 0 || len(plan.Unchanged) != 2 {
		t.Errorf("re-apply plan = %+v\n%s", plan, out.Compose)
	}

	// A change, a removed app and a removed database.
	changed := `
services:
  web:
    image: ghcr.io/me/shop:2
    deploy: {replicas: 3}
`
	plan, err = c.ApplyCompose(ctx, p.ID, []byte(changed), ApplyOptions{DryRun: true, Prune: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Update) != 1 || !slices.Contains(plan.Update[0].Fields, "image") || !slices.Contains(plan.Update[0].Fields, "replicas") ||
		!slices.Contains(plan.Update[0].Fields, "uptime") ||
		!slices.Equal(plan.Orphaned, []string{"db"}) || len(plan.Delete) != 0 {
		t.Errorf("change plan = %+v", plan)
	}

	// Only the check changes: no update of the service, no deploy.
	paused := strings.Replace(string(out.Compose), "interval: 2m0s", "interval: 2m0s\n        paused: true", 1)
	plan, err = c.ApplyCompose(ctx, p.ID, []byte(paused), ApplyOptions{})
	if err != nil || len(plan.Update) != 1 || !slices.Equal(plan.Update[0].Fields, []string{"uptime"}) {
		t.Fatalf("pause plan = %+v, %v\n%s", plan, err, paused)
	}
	if chk, _ := c.store.GetUptimeCheck(ctx, web.ID); chk.Enabled {
		t.Error("check not paused")
	}
	// A check on a service without a domain is refused.
	noDomain := strings.Replace(paused, "domain: shop.example.com", "", 1)
	if _, err := c.ApplyCompose(ctx, p.ID, []byte(noDomain), ApplyOptions{DryRun: true}); err == nil || !strings.Contains(err.Error(), "uptime") {
		t.Errorf("no domain: %v", err)
	}

	// Errors are reported before any change.
	bad := "services:\n  web:\n    image: ghcr.io/me/shop:3\n    environment: {X: '{{ db.nope.URL }}'}\n  new:\n    image: nginx\n"
	if _, err := c.ApplyCompose(ctx, p.ID, []byte(bad), ApplyOptions{}); err == nil || !strings.Contains(err.Error(), "db.nope") {
		t.Errorf("err = %v", err)
	}
	if svcs, _ := c.store.ListServices(ctx, p.ID); len(svcs) != 2 {
		t.Errorf("a failed apply changed services: %d", len(svcs))
	}
}

func TestComposeStandardKeys(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	p, err := c.store.CreateProject(ctx, store.Project{Name: "shop", ServerID: store.LocalServerID,
		Env: map[string]string{"SHARED": "s"}})
	if err != nil {
		t.Fatal(err)
	}
	src := `
services:
  web:
    image: ghcr.io/me/shop:1
    expose: ["3000"]
    healthcheck:
      test: ["CMD", "wget", "-qO-", "http://localhost:3000/healthz"]
      interval: 30s
    environment:
      API_KEY: ${API_KEY}
      SHARED: ${SHARED}
      LOG_LEVEL: info
    x-kipitiny:
      domain: shop.example.com
`
	if _, err := c.ApplyCompose(ctx, p.ID, []byte(src), ApplyOptions{Env: map[string]string{"API_KEY": "k3y"}}); err != nil {
		t.Fatal(err)
	}
	svcs, _ := c.store.ListServices(ctx, p.ID)
	web := svcs[0]
	if web.Port != 3000 || web.Env["SHARED"] != "{{ project.SHARED }}" {
		t.Errorf("web = %+v", web)
	}
	// ${API_KEY} came from the .env: a secret. ${SHARED} became a project
	// reference, which keeps the project's own flag.
	if !slices.Equal(web.Secrets, []string{"API_KEY"}) {
		t.Errorf("secrets = %v", web.Secrets)
	}
	if h := web.Healthcheck; !slices.Equal(h.Test, []string{"CMD", "wget", "-qO-", "http://localhost:3000/healthz"}) || h.IntervalSeconds != 30 {
		t.Errorf("healthcheck = %+v", h)
	}

	spec := containerSpec(p, web, envSources{project: p.Env}, "D1", 1, route{})
	if hc := spec.Config.Healthcheck; hc == nil || hc.Test[0] != "CMD" || hc.StartPeriod != readyTimeout || hc.StartInterval != time.Second {
		t.Errorf("container healthcheck = %+v", hc)
	}
	if readinessMode(web) != "its healthcheck passing" {
		t.Errorf("readiness = %s", readinessMode(web))
	}

	out, err := c.ExportCompose(ctx, p.ID, ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	text := string(out.Compose)
	for _, want := range []string{"expose:", `- "3000"`, "healthcheck:", "interval: 30s", "API_KEY: ${WEB_API_KEY}"} {
		if !strings.Contains(text, want) {
			t.Errorf("export lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "secrets:") || strings.Contains(text, "port:") {
		t.Errorf("export still lists secrets or x-kipitiny.port:\n%s", text)
	}
	plan, err := c.ApplyCompose(ctx, p.ID, out.Compose, ApplyOptions{DryRun: true})
	if err != nil || !slices.Equal(plan.Unchanged, []string{"web"}) {
		t.Errorf("own export re-applied: %+v, %v\n%s", plan, err, text)
	}

	bad := store.Service{Kind: store.ServiceKindApp, Image: "x", Replicas: 1, Port: 80, HealthPath: "/h",
		Healthcheck: store.Healthcheck{Test: []string{"CMD", "true"}}}
	if err := validateService(bad); !errors.Is(err, ErrInvalid) {
		t.Errorf("health path and healthcheck: %v", err)
	}
	bad.HealthPath, bad.Healthcheck.Test = "", []string{"wget"}
	if err := validateService(bad); !errors.Is(err, ErrInvalid) {
		t.Errorf("test without CMD: %v", err)
	}
}
