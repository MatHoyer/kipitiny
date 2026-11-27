package data

import (
	"errors"
	"testing"

	"github.com/kipit-labs/kipitiny/internal/domain"
)

func redisService() domain.Service {
	return domain.Service{
		ID: "01CACHE", ProjectID: "P1", Name: "cache", Kind: domain.ServiceKindRedis,
		Image: DefaultRedisImage, Replicas: 1, MemoryMB: 256, Env: NewRedisEnv(),
	}
}

func TestCheckRedisUpdate(t *testing.T) {
	old := redisService()
	upgrade := old
	upgrade.Image = "redis:8.2-alpine"
	upgrade.MemoryMB = 512
	if err := CheckDatabaseUpdate(old, upgrade); err != nil {
		t.Errorf("image/memory change refused: %v", err)
	}
	creds := old
	creds.Env = NewRedisEnv()
	if err := CheckDatabaseUpdate(old, creds); !errors.Is(err, ErrInvalid) {
		t.Errorf("password change allowed: %v", err)
	}
}
