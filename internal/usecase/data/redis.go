package data

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kipit-labs/kipitiny/internal/usecase/identity"

	"github.com/kipit-labs/kipitiny/internal/domain"
)

const (
	DefaultRedisImage    = "redis:8-alpine"
	DefaultRedisMemoryMB = 256
	minRedisMemoryMB     = 32

	RedisPassword    = "REDIS_PASSWORD"
	redisVolumeMount = "/data"
	redisPort        = 6379

	// Redis writes its AOF/RDB on shutdown.
	redisStopTimeout = 30 * time.Second
)

// redisPasswordRe keeps the password safe to pass as an argument and in a URL
// (randomToken's alphabet).
var redisPasswordRe = regexp.MustCompile(`^[A-Za-z0-9_-]{16,}$`)

func NewRedisEnv() map[string]string {
	return map[string]string{RedisPassword: identity.RandomPassword()}
}

// RedisVolume is the named volume holding a redis service's data.
func RedisVolume(serviceID string) string {
	return "kipitiny-redisdata-" + strings.ToLower(serviceID)
}

func validateRedis(s domain.Service) error {
	if s.MemoryMB < minRedisMemoryMB {
		return fmt.Errorf("%w: redis needs at least %d MB of memory", ErrInvalid, minRedisMemoryMB)
	}
	if !redisPasswordRe.MatchString(s.Env[RedisPassword]) {
		return fmt.Errorf("%w: %s is required", ErrInvalid, RedisPassword)
	}
	return nil
}

func redisURL(db domain.Service) string {
	u := url.URL{
		Scheme: "redis",
		User:   url.UserPassword("", db.Env[RedisPassword]),
		Host:   db.Name + ":" + strconv.Itoa(redisPort),
	}
	return u.String()
}

// redisField resolves {{ db.NAME.FIELD }} for a redis service; it has no
// DATABASE.
func redisField(db domain.Service, field string) (string, bool) {
	switch field {
	case "URL":
		return redisURL(db), true
	case "HOST":
		return db.Name, true
	case "PORT":
		return strconv.Itoa(redisPort), true
	case "USER":
		return "default", true
	case "PASSWORD":
		return db.Env[RedisPassword], true
	}
	return "", false
}

// redisArgs persists every write (AOF) and caps memory below the container
// limit, so redis refuses writes instead of being OOM-killed (the rest is
// headroom for the AOF rewrite fork and allocator overhead).
func redisArgs(svc domain.Service) []string {
	args := []string{
		"redis-server",
		"--appendonly", "yes",
		"--dir", redisVolumeMount,
		"--requirepass", svc.Env[RedisPassword],
	}
	if svc.MemoryMB > 0 {
		args = append(args, "--maxmemory", fmt.Sprintf("%dmb", svc.MemoryMB*3/4))
	}
	return args
}

func applyRedisSpec(spec *domain.ContainerSpec, svc domain.Service) {
	spec.Cmd = redisArgs(svc)
	// redis-cli reads it: the healthcheck and the web terminal need no -a.
	spec.Env = append(spec.Env, "REDISCLI_AUTH="+svc.Env[RedisPassword])
	spec.Healthcheck = &domain.HealthConfig{
		// PING answers LOADING while the AOF replays: not ready yet.
		Test:          []string{"CMD-SHELL", "redis-cli ping | grep -q PONG"},
		Interval:      10 * time.Second,
		Timeout:       5 * time.Second,
		StartPeriod:   30 * time.Second,
		StartInterval: time.Second,
		Retries:       3,
	}
	spec.Mounts = append(spec.Mounts, domain.Mount{
		Type:   domain.MountVolume,
		Source: RedisVolume(svc.ID),
		Target: redisVolumeMount,
	})
	spec.Labels[domain.LabelComponent] = string(domain.ServiceKindRedis)
}
