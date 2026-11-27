package core

import (
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/usecase/data"
	"github.com/kipit-labs/kipitiny/internal/usecase/network"
)

func TestValidatePostgres(t *testing.T) {
	if err := validateService(pgService()); err != nil {
		t.Fatalf("valid service rejected: %v", err)
	}
	for name, mutate := range map[string]func(*domain.Service){
		"replicas":         func(s *domain.Service) { s.Replicas = 2 },
		"public":           func(s *domain.Service) { s.Domain, s.Port = "db.example.com", 5432 },
		"low memory":       func(s *domain.Service) { s.MemoryMB = 64 },
		"unpinned":         func(s *domain.Service) { s.Image = "postgres:latest" },
		"no password":      func(s *domain.Service) { delete(s.Env, data.PGPassword) },
		"db reference":     func(s *domain.Service) { s.Env["OTHER"] = "{{ project.A }}/{{ db.other.URL }}" },
		"secret reference": func(s *domain.Service) { s.Env["OTHER"] = "{{ pass://V/I/password }}" },
	} {
		s := pgService()
		mutate(&s)
		if err := validateService(s); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: got %v, want ErrInvalid", name, err)
		}
	}
}

func TestCheckDatabaseUpdate(t *testing.T) {
	old := pgService()
	old.Image = "postgres:17-alpine"

	minor := old
	minor.Image = "postgres:17.2-alpine"
	if err := data.CheckDatabaseUpdate(old, minor); err != nil {
		t.Errorf("minor upgrade refused: %v", err)
	}

	major := old
	major.Image = "postgres:18-alpine"
	if err := data.CheckDatabaseUpdate(old, major); !errors.Is(err, ErrInvalid) {
		t.Errorf("major upgrade allowed: %v", err)
	}

	for image, ok := range map[string]bool{
		"postgis/postgis:17-3.6-alpine":    true,
		"postgres:17.6-alpine3.22":         true,
		"postgres:17":                      false,
		"postgres:17-bookworm":             false,
		"postgis/postgis:17-3.6":           false,
		"postgres:17-alpine@sha256:" + sha: true,
		"ghcr.io/org/postgres:17-custom":   false,
	} {
		libc := old
		libc.Image = image
		if err := data.CheckDatabaseUpdate(old, libc); (err == nil) != ok {
			t.Errorf("%s → %s: %v", old.Image, image, err)
		}
	}

	creds := old
	creds.Env = map[string]string{data.PGUser: "app", data.PGPassword: "changed", data.PGDatabase: "app"}
	if err := data.CheckDatabaseUpdate(old, creds); !errors.Is(err, ErrInvalid) {
		t.Errorf("password change allowed: %v", err)
	}

	extra := old
	extra.Env = map[string]string{"TZ": "Europe/Paris"}
	maps.Copy(extra.Env, old.Env)
	if err := data.CheckDatabaseUpdate(old, extra); !errors.Is(err, ErrInvalid) {
		t.Errorf("env change allowed: %v", err)
	}
}

func TestPostgresContainerSpec(t *testing.T) {
	p := domain.Project{ID: "P1", Name: "shop"}
	svc := pgService()
	svc.CPUs = 1.5
	spec := containerSpec(p, svc, envSources{project: p.Env}, "D1", 1, network.Routing{Resolver: network.CertResolverTLS})

	if spec.HostConfig.Memory != 512<<20 {
		t.Errorf("memory = %d", spec.HostConfig.Memory)
	}
	if spec.HostConfig.NanoCPUs != 1_500_000_000 {
		t.Errorf("nano cpus = %d", spec.HostConfig.NanoCPUs)
	}
	if !slices.Contains(spec.Config.Env, "PGDATA="+data.PGDataDir) {
		t.Errorf("PGDATA missing: %v", spec.Config.Env)
	}
	if !slices.Contains(spec.Config.Cmd, "shared_buffers=128MB") {
		t.Errorf("tuning missing: %v", spec.Config.Cmd)
	}
	if spec.Config.Healthcheck == nil || !strings.Contains(spec.Config.Healthcheck.Test[1], "pg_isready") {
		t.Error("healthcheck missing")
	}
	if len(spec.HostConfig.Mounts) != 1 || spec.HostConfig.Mounts[0].Source != data.PostgresVolume("01DB") {
		t.Errorf("mounts = %v", spec.HostConfig.Mounts)
	}
	if _, public := spec.Config.Labels["traefik.enable"]; public {
		t.Error("database exposed to traefik")
	}
}

func TestDatabaseRefsInSpec(t *testing.T) {
	p := domain.Project{ID: "P1", Name: "shop"}
	db := pgService()
	app := domain.Service{ID: "01APP", Name: "web", Image: "app", Replicas: 1,
		Env: map[string]string{"DATABASE_URL": "{{ db." + db.Name + ".URL }}"}}

	spec := containerSpec(p, app, envSources{project: p.Env, dbs: map[string]domain.Service{db.Name: db}}, "D1", 1, network.Routing{Resolver: network.CertResolverTLS})
	if !slices.Contains(spec.Config.Env, "DATABASE_URL="+data.DatabaseURL(db)) {
		t.Errorf("DATABASE_URL not resolved: %v", spec.Config.Env)
	}
}
