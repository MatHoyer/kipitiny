package core

import (
	"strings"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/compose"
	"github.com/MatHoyer/kipitiny/internal/store"
)

func TestExportFile(t *testing.T) {
	p := store.Project{Name: "shop", Env: map[string]string{"REGION": "eu", "TOKEN": "t0k"}, Secrets: []string{"TOKEN"}}
	svcs := []store.Service{
		{
			Name: "web", Kind: store.ServiceKindApp, Image: "ghcr.io/me/shop:1", Replicas: 2, Port: 3000, Domain: "shop.example.com",
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
	f, env := exportFile(p, svcs, true)
	web := f.Services["web"]
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
