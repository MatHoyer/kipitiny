package core

import (
	"errors"
	"slices"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/store"
)

func redisService() store.Service {
	return store.Service{
		ID: "01CACHE", ProjectID: "P1", Name: "cache", Kind: store.ServiceKindRedis,
		Image: DefaultRedisImage, Replicas: 1, MemoryMB: 256, Env: newRedisEnv(),
	}
}

func TestValidateRedis(t *testing.T) {
	if err := validateService(redisService()); err != nil {
		t.Fatalf("valid service rejected: %v", err)
	}
	for name, mutate := range map[string]func(*store.Service){
		"replicas":     func(s *store.Service) { s.Replicas = 2 },
		"public":       func(s *store.Service) { s.Domain, s.Port = "cache.example.com", 6379 },
		"low memory":   func(s *store.Service) { s.MemoryMB = 16 },
		"no password":  func(s *store.Service) { delete(s.Env, redisPassword) },
		"bad password": func(s *store.Service) { s.Env[redisPassword] = "has space and is long" },
		"db reference": func(s *store.Service) { s.Env["OTHER"] = "{{ db.other.URL }}" },
		"pre-deploy":   func(s *store.Service) { s.PreDeploy = "true" },
	} {
		s := redisService()
		mutate(&s)
		if err := validateService(s); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: got %v, want ErrInvalid", name, err)
		}
	}
}

func TestCheckRedisUpdate(t *testing.T) {
	old := redisService()
	upgrade := old
	upgrade.Image = "redis:8.2-alpine"
	upgrade.MemoryMB = 512
	if err := checkDatabaseUpdate(old, upgrade); err != nil {
		t.Errorf("image/memory change refused: %v", err)
	}
	creds := old
	creds.Env = newRedisEnv()
	if err := checkDatabaseUpdate(old, creds); !errors.Is(err, ErrInvalid) {
		t.Errorf("password change allowed: %v", err)
	}
}

func TestRedisRefs(t *testing.T) {
	db := redisService()
	db.Env[redisPassword] = "s3cr3t-pass_word0"
	src := envSources{dbs: map[string]store.Service{db.Name: db}}
	for ref, want := range map[string]string{
		"{{ db.cache.URL }}":      "redis://:s3cr3t-pass_word0@cache:6379",
		"{{ db.cache.HOST }}":     "cache",
		"{{ db.cache.PORT }}":     "6379",
		"{{ db.cache.PASSWORD }}": "s3cr3t-pass_word0",
	} {
		if got := resolveEnv(map[string]string{"V": ref}, src)["V"]; got != want {
			t.Errorf("%s = %q, want %q", ref, got, want)
		}
	}
	if err := checkRefs(map[string]string{"V": "{{ db.cache.DATABASE }}"}, src); !errors.Is(err, ErrInvalid) {
		t.Errorf("redis DATABASE accepted: %v", err)
	}
}

func TestRedisContainerSpec(t *testing.T) {
	p := store.Project{ID: "P1", Name: "shop"}
	svc := redisService()
	spec := containerSpec(p, svc, envSources{project: p.Env}, "D1", 1, route{resolver: certResolver})

	if spec.Name != "shop-cache-1" {
		t.Errorf("name = %q, want no deploy suffix", spec.Name)
	}
	if spec.HostConfig.Memory != 256<<20 {
		t.Errorf("memory = %d", spec.HostConfig.Memory)
	}
	cmd := spec.Config.Cmd
	if i := slices.Index(cmd, "--requirepass"); i < 0 || cmd[i+1] != svc.Env[redisPassword] {
		t.Errorf("password not set: %v", cmd)
	}
	if i := slices.Index(cmd, "--maxmemory"); i < 0 || cmd[i+1] != "192mb" {
		t.Errorf("maxmemory not set: %v", cmd)
	}
	if !slices.Contains(spec.Config.Env, "REDISCLI_AUTH="+svc.Env[redisPassword]) {
		t.Errorf("REDISCLI_AUTH missing: %v", spec.Config.Env)
	}
	if spec.Config.Healthcheck == nil {
		t.Error("healthcheck missing")
	}
	if len(spec.HostConfig.Mounts) != 1 || spec.HostConfig.Mounts[0].Source != RedisVolume(svc.ID) || DataVolume(svc) != RedisVolume(svc.ID) {
		t.Errorf("mounts = %v", spec.HostConfig.Mounts)
	}
	if _, public := spec.Config.Labels["traefik.enable"]; public {
		t.Error("database exposed to traefik")
	}
}
