package deploy

import (
	"errors"
	"maps"
	"testing"

	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/usecase/data"
	"github.com/kipit-labs/kipitiny/internal/usecase/network"
)

func TestValidateMySQLAndMongo(t *testing.T) {
	for _, base := range []domain.Service{mysqlService(domain.ServiceKindMySQL), mysqlService(domain.ServiceKindMariaDB), mongoService()} {
		if err := validateService(base); err != nil {
			t.Fatalf("%s: valid service rejected: %v", base.Kind, err)
		}
		pw := databasePasswordKey(base.Kind)
		for name, mutate := range map[string]func(*domain.Service){
			"replicas":     func(s *domain.Service) { s.Replicas = 2 },
			"public":       func(s *domain.Service) { s.Domain, s.Port = "db.example.com", 3306 },
			"low memory":   func(s *domain.Service) { s.MemoryMB = 64 },
			"no password":  func(s *domain.Service) { delete(s.Env, pw) },
			"db reference": func(s *domain.Service) { s.Env["OTHER"] = "{{ db.other.URL }}" },
		} {
			s := base
			s.Env = maps.Clone(base.Env)
			mutate(&s)
			if err := validateService(s); !errors.Is(err, ErrInvalid) {
				t.Errorf("%s %s: got %v, want ErrInvalid", base.Kind, name, err)
			}
		}
		creds := base
		creds.Env = maps.Clone(base.Env)
		creds.Env[pw] = "changed"
		if err := data.CheckDatabaseUpdate(base, creds); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: password change allowed: %v", base.Kind, err)
		}
	}
	root := mysqlService(domain.ServiceKindMySQL)
	delete(root.Env, data.MySQLRootPassword)
	if err := validateService(root); !errors.Is(err, ErrInvalid) {
		t.Errorf("no root password: %v", err)
	}
}

func TestMySQLAndMongoRefs(t *testing.T) {
	sql := mysqlService(domain.ServiceKindMariaDB)
	sql.Env[data.MySQLPassword] = "p@ss"
	docs := mongoService()
	docs.Env[data.MongoPassword] = "s3cr3t"
	src := envSources{dbs: map[string]domain.Service{sql.Name: sql, docs.Name: docs}}
	for ref, want := range map[string]string{
		"{{ db.sql.URL }}":       "mysql://app:p%40ss@sql:3306/app",
		"{{ db.sql.PORT }}":      "3306",
		"{{ db.sql.USER }}":      "app",
		"{{ db.sql.DATABASE }}":  "app",
		"{{ db.docs.URL }}":      "mongodb://app:s3cr3t@docs:27017/app?authSource=admin",
		"{{ db.docs.HOST }}":     "docs",
		"{{ db.docs.PORT }}":     "27017",
		"{{ db.docs.PASSWORD }}": "s3cr3t",
	} {
		if got := resolveEnv(map[string]string{"V": ref}, src)["V"]; got != want {
			t.Errorf("%s = %q, want %q", ref, got, want)
		}
	}
}

func TestMySQLAndMongoContainerSpec(t *testing.T) {
	p := domain.Project{ID: "P1", Name: "shop"}
	for _, svc := range []domain.Service{mysqlService(domain.ServiceKindMySQL), mysqlService(domain.ServiceKindMariaDB), mongoService()} {
		spec := containerSpec(p, svc, envSources{project: p.Env}, "D1", 1, network.Routing{Resolver: network.CertResolverTLS})
		if spec.Name != "shop-"+svc.Name+"-1" {
			t.Errorf("%s: name = %q, want no deploy suffix", svc.Kind, spec.Name)
		}
		if spec.Healthcheck == nil || len(spec.Cmd) == 0 || spec.Cmd[0][0] != '-' {
			t.Errorf("%s: cmd %v, healthcheck %v", svc.Kind, spec.Cmd, spec.Healthcheck)
		}
		if len(spec.Mounts) != 1 || spec.Mounts[0].Source != data.DataVolume(svc) || data.DataVolume(svc) == "" {
			t.Errorf("%s: mounts = %v", svc.Kind, spec.Mounts)
		}
		if spec.Labels["kipitiny.component"] != string(svc.Kind) {
			t.Errorf("%s: labels = %v", svc.Kind, spec.Labels)
		}
		if _, public := spec.Labels["traefik.enable"]; public {
			t.Errorf("%s: database exposed to traefik", svc.Kind)
		}
	}
	// Mongo's config VOLUME would leave an anonymous volume per recreate.
	spec := containerSpec(p, mongoService(), envSources{}, "D1", 1, network.Routing{Resolver: network.CertResolverTLS})
	if _, ok := spec.Tmpfs[data.MongoConfigMount]; !ok {
		t.Errorf("tmpfs = %v", spec.Tmpfs)
	}
}
