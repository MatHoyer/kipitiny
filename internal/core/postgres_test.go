package core

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/store"
)

func TestPostgresMajor(t *testing.T) {
	for image, want := range map[string]int{
		"postgres:17-alpine":         17,
		"postgres:16.4":              16,
		"postgres:18":                18,
		"postgis/postgis:16-3.4":     16,
		"postgres":                   0, // implicit latest
		"postgres:latest":            0,
		"postgres@sha256:" + sha:     0,
		"ghcr.io/org/pg:15-bookworm": 15,
	} {
		if got := postgresMajor(image); got != want {
			t.Errorf("postgresMajor(%q) = %d, want %d", image, got, want)
		}
	}
}

func pgService() store.Service {
	return store.Service{
		ID: "01DB", ProjectID: "P1", Name: "db", Kind: store.ServiceKindPostgres,
		Image: DefaultPostgresImage, Replicas: 1, MemoryMB: 512, Env: newPostgresEnv(),
	}
}

func TestValidatePostgres(t *testing.T) {
	if err := validateService(pgService()); err != nil {
		t.Fatalf("valid service rejected: %v", err)
	}
	for name, mutate := range map[string]func(*store.Service){
		"replicas":     func(s *store.Service) { s.Replicas = 2 },
		"public":       func(s *store.Service) { s.Domain, s.Port = "db.example.com", 5432 },
		"low memory":   func(s *store.Service) { s.MemoryMB = 64 },
		"unpinned":     func(s *store.Service) { s.Image = "postgres:latest" },
		"no password":  func(s *store.Service) { delete(s.Env, pgPassword) },
		"db reference": func(s *store.Service) { s.Env["OTHER"] = "{{ project.A }}/{{ db.other.URL }}" },
	} {
		s := pgService()
		mutate(&s)
		if err := validateService(s); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: got %v, want ErrInvalid", name, err)
		}
	}
}

func TestCheckPostgresUpdate(t *testing.T) {
	old := pgService()

	minor := old
	minor.Image = "postgres:17.2-alpine"
	if err := checkPostgresUpdate(old, minor); err != nil {
		t.Errorf("minor upgrade refused: %v", err)
	}

	major := old
	major.Image = "postgres:18-alpine"
	if err := checkPostgresUpdate(old, major); !errors.Is(err, ErrInvalid) {
		t.Errorf("major upgrade allowed: %v", err)
	}

	creds := old
	creds.Env = map[string]string{pgUser: "app", pgPassword: "changed", pgDatabase: "app"}
	if err := checkPostgresUpdate(old, creds); !errors.Is(err, ErrInvalid) {
		t.Errorf("password change allowed: %v", err)
	}
}

func TestDatabaseURLEscapes(t *testing.T) {
	db := pgService()
	db.Env[pgPassword] = "p@ss/w:rd"
	got := DatabaseURL(db)
	if got != "postgres://app:p%40ss%2Fw%3Ard@db:5432/app" {
		t.Errorf("DatabaseURL = %q", got)
	}
}

func TestPostgresContainerSpec(t *testing.T) {
	p := store.Project{ID: "P1", Name: "shop"}
	spec := containerSpec(p, pgService(), nil, "D1", 1, certResolver)

	if spec.HostConfig.Memory != 512<<20 {
		t.Errorf("memory = %d", spec.HostConfig.Memory)
	}
	if !slices.Contains(spec.Config.Env, "PGDATA="+pgDataDir) {
		t.Errorf("PGDATA missing: %v", spec.Config.Env)
	}
	if !slices.Contains(spec.Config.Cmd, "shared_buffers=128MB") {
		t.Errorf("tuning missing: %v", spec.Config.Cmd)
	}
	if spec.Config.Healthcheck == nil || !strings.Contains(spec.Config.Healthcheck.Test[1], "pg_isready") {
		t.Error("healthcheck missing")
	}
	if len(spec.HostConfig.Mounts) != 1 || spec.HostConfig.Mounts[0].Source != PostgresVolume("01DB") {
		t.Errorf("mounts = %v", spec.HostConfig.Mounts)
	}
	if _, public := spec.Config.Labels["traefik.enable"]; public {
		t.Error("database exposed to traefik")
	}
}

func TestDatabaseRefsInSpec(t *testing.T) {
	p := store.Project{ID: "P1", Name: "shop"}
	db := pgService()
	app := store.Service{ID: "01APP", Name: "web", Image: "app", Replicas: 1,
		Env: map[string]string{"DATABASE_URL": "{{ db." + db.Name + ".URL }}"}}

	spec := containerSpec(p, app, map[string]store.Service{db.Name: db}, "D1", 1, certResolver)
	if !slices.Contains(spec.Config.Env, "DATABASE_URL="+DatabaseURL(db)) {
		t.Errorf("DATABASE_URL not resolved: %v", spec.Config.Env)
	}
}

func TestParseVerifyOutput(t *testing.T) {
	d, err := parseVerifyOutput("3|100000|8421376\n")
	if err != nil || d.Tables != 3 || d.Rows != 100000 || d.DBBytes != 8421376 {
		t.Fatalf("got %+v %v", d, err)
	}
	if _, err := parseVerifyOutput("ERROR"); err == nil {
		t.Fatal("garbage accepted")
	}
}
