package core

import (
	"errors"
	"maps"
	"slices"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/store"
)

func mysqlService(kind store.ServiceKind) store.Service {
	return store.Service{
		ID: "01SQL", ProjectID: "P1", Name: "sql", Kind: kind,
		Image: defaultMySQLImage(kind), Replicas: 1, MemoryMB: 512, Env: newMySQLEnv(),
	}
}

func mongoService() store.Service {
	return store.Service{
		ID: "01DOCS", ProjectID: "P1", Name: "docs", Kind: store.ServiceKindMongoDB,
		Image: DefaultMongoImage, Replicas: 1, MemoryMB: 1024, Env: newMongoEnv(),
	}
}

func TestValidateMySQLAndMongo(t *testing.T) {
	for _, base := range []store.Service{mysqlService(store.ServiceKindMySQL), mysqlService(store.ServiceKindMariaDB), mongoService()} {
		if err := validateService(base); err != nil {
			t.Fatalf("%s: valid service rejected: %v", base.Kind, err)
		}
		pw := databasePasswordKey(base.Kind)
		for name, mutate := range map[string]func(*store.Service){
			"replicas":     func(s *store.Service) { s.Replicas = 2 },
			"public":       func(s *store.Service) { s.Domain, s.Port = "db.example.com", 3306 },
			"low memory":   func(s *store.Service) { s.MemoryMB = 64 },
			"no password":  func(s *store.Service) { delete(s.Env, pw) },
			"db reference": func(s *store.Service) { s.Env["OTHER"] = "{{ db.other.URL }}" },
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
		if err := checkDatabaseUpdate(base, creds); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: password change allowed: %v", base.Kind, err)
		}
	}
	root := mysqlService(store.ServiceKindMySQL)
	delete(root.Env, mysqlRootPassword)
	if err := validateService(root); !errors.Is(err, ErrInvalid) {
		t.Errorf("no root password: %v", err)
	}
}

func TestMySQLAndMongoRefs(t *testing.T) {
	sql := mysqlService(store.ServiceKindMariaDB)
	sql.Env[mysqlPassword] = "p@ss"
	docs := mongoService()
	docs.Env[mongoPassword] = "s3cr3t"
	src := envSources{dbs: map[string]store.Service{sql.Name: sql, docs.Name: docs}}
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
	p := store.Project{ID: "P1", Name: "shop"}
	for _, svc := range []store.Service{mysqlService(store.ServiceKindMySQL), mysqlService(store.ServiceKindMariaDB), mongoService()} {
		spec := containerSpec(p, svc, envSources{project: p.Env}, "D1", 1, route{resolver: certResolver})
		if spec.Name != "shop-"+svc.Name+"-1" {
			t.Errorf("%s: name = %q, want no deploy suffix", svc.Kind, spec.Name)
		}
		if spec.Config.Healthcheck == nil || len(spec.Config.Cmd) == 0 || spec.Config.Cmd[0][0] != '-' {
			t.Errorf("%s: cmd %v, healthcheck %v", svc.Kind, spec.Config.Cmd, spec.Config.Healthcheck)
		}
		if len(spec.HostConfig.Mounts) != 1 || spec.HostConfig.Mounts[0].Source != DataVolume(svc) || DataVolume(svc) == "" {
			t.Errorf("%s: mounts = %v", svc.Kind, spec.HostConfig.Mounts)
		}
		if spec.Config.Labels["kipitiny.component"] != string(svc.Kind) {
			t.Errorf("%s: labels = %v", svc.Kind, spec.Config.Labels)
		}
		if _, public := spec.Config.Labels["traefik.enable"]; public {
			t.Errorf("%s: database exposed to traefik", svc.Kind)
		}
		if k, err := backupKind(svc); k != store.BackupKindDump || err != nil || volumeNames(svc) != nil {
			t.Errorf("%s: backed up as %q (%v), volumes %v", svc.Kind, k, err, volumeNames(svc))
		}
	}
	if cmd := mysqlArgs(512); !slices.Contains(cmd, "--innodb-buffer-pool-size=256M") {
		t.Errorf("mysql args = %v", cmd)
	}
	for mem, want := range map[int]string{512: "0.25", 1024: "0.25", 4096: "1.50"} {
		if got := mongoArgs(mem); got[1] != want {
			t.Errorf("mongo cache for %d MB = %s, want %s", mem, got[1], want)
		}
	}
	// Mongo's config VOLUME would leave an anonymous volume per recreate.
	spec := containerSpec(p, mongoService(), envSources{}, "D1", 1, route{resolver: certResolver})
	if _, ok := spec.HostConfig.Tmpfs[mongoConfigMount]; !ok {
		t.Errorf("tmpfs = %v", spec.HostConfig.Tmpfs)
	}
}

func TestMySQLDumpCmd(t *testing.T) {
	if cmd := mysqlDumpCmd(store.ServiceKindMariaDB, "app"); cmd[0] != "mariadb-dump" || slices.Contains(cmd, "--set-gtid-purged=OFF") ||
		cmd[len(cmd)-1] != "app" || cmd[len(cmd)-2] != "--" {
		t.Errorf("mariadb = %v", cmd)
	}
	if cmd := mysqlDumpCmd(store.ServiceKindMySQL, "app"); cmd[0] != "mysqldump" || !slices.Contains(cmd, "--set-gtid-purged=OFF") {
		t.Errorf("mysql = %v", cmd)
	}
}
