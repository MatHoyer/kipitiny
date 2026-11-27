package deploy

import (
	"errors"
	"slices"
	"testing"

	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/usecase/data"
	"github.com/kipit-labs/kipitiny/internal/usecase/network"
)

func TestValidateRedis(t *testing.T) {
	if err := validateService(redisService()); err != nil {
		t.Fatalf("valid service rejected: %v", err)
	}
	for name, mutate := range map[string]func(*domain.Service){
		"replicas":     func(s *domain.Service) { s.Replicas = 2 },
		"public":       func(s *domain.Service) { s.Domain, s.Port = "cache.example.com", 6379 },
		"low memory":   func(s *domain.Service) { s.MemoryMB = 16 },
		"no password":  func(s *domain.Service) { delete(s.Env, data.RedisPassword) },
		"bad password": func(s *domain.Service) { s.Env[data.RedisPassword] = "has space and is long" },
		"db reference": func(s *domain.Service) { s.Env["OTHER"] = "{{ db.other.URL }}" },
		"pre-deploy":   func(s *domain.Service) { s.PreDeploy = "true" },
	} {
		s := redisService()
		mutate(&s)
		if err := validateService(s); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: got %v, want ErrInvalid", name, err)
		}
	}
}

func TestRedisRefs(t *testing.T) {
	db := redisService()
	db.Env[data.RedisPassword] = "s3cr3t-pass_word0"
	src := envSources{dbs: map[string]domain.Service{db.Name: db}}
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
	p := domain.Project{ID: "P1", Name: "shop"}
	svc := redisService()
	spec := containerSpec(p, svc, envSources{project: p.Env}, "D1", 1, network.Routing{Resolver: network.CertResolverTLS})

	if spec.Name != "shop-cache-1" {
		t.Errorf("name = %q, want no deploy suffix", spec.Name)
	}
	if spec.Memory != 256<<20 {
		t.Errorf("memory = %d", spec.Memory)
	}
	cmd := spec.Cmd
	if i := slices.Index(cmd, "--requirepass"); i < 0 || cmd[i+1] != svc.Env[data.RedisPassword] {
		t.Errorf("password not set: %v", cmd)
	}
	if i := slices.Index(cmd, "--maxmemory"); i < 0 || cmd[i+1] != "192mb" {
		t.Errorf("maxmemory not set: %v", cmd)
	}
	if !slices.Contains(spec.Env, "REDISCLI_AUTH="+svc.Env[data.RedisPassword]) {
		t.Errorf("REDISCLI_AUTH missing: %v", spec.Env)
	}
	if spec.Healthcheck == nil {
		t.Error("healthcheck missing")
	}
	if len(spec.Mounts) != 1 || spec.Mounts[0].Source != data.RedisVolume(svc.ID) || data.DataVolume(svc) != data.RedisVolume(svc.ID) {
		t.Errorf("mounts = %v", spec.Mounts)
	}
	if _, public := spec.Labels["traefik.enable"]; public {
		t.Error("database exposed to traefik")
	}
}
