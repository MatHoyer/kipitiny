package core

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"

	"github.com/MatHoyer/kipitiny/internal/docker"
	"github.com/MatHoyer/kipitiny/internal/store"
)

const (
	DefaultRedisImage    = "redis:8-alpine"
	defaultRedisMemoryMB = 256
	minRedisMemoryMB     = 32

	redisPassword    = "REDIS_PASSWORD"
	redisVolumeMount = "/data"
	redisPort        = 6379

	// Redis writes its AOF/RDB on shutdown.
	redisStopTimeout = 30 * time.Second
)

// redisPasswordRe keeps the password safe to pass as an argument and in a URL
// (randomToken's alphabet).
var redisPasswordRe = regexp.MustCompile(`^[A-Za-z0-9_-]{16,}$`)

func newRedisEnv() map[string]string {
	return map[string]string{redisPassword: randomToken(24)}
}

// RedisVolume is the named volume holding a redis service's data.
func RedisVolume(serviceID string) string {
	return "kipitiny-redisdata-" + strings.ToLower(serviceID)
}

func validateRedis(s store.Service) error {
	if s.MemoryMB < minRedisMemoryMB {
		return fmt.Errorf("%w: redis needs at least %d MB of memory", ErrInvalid, minRedisMemoryMB)
	}
	if !redisPasswordRe.MatchString(s.Env[redisPassword]) {
		return fmt.Errorf("%w: %s is required", ErrInvalid, redisPassword)
	}
	return nil
}

func redisURL(db store.Service) string {
	u := url.URL{
		Scheme: "redis",
		User:   url.UserPassword("", db.Env[redisPassword]),
		Host:   db.Name + ":" + strconv.Itoa(redisPort),
	}
	return u.String()
}

// redisField resolves {{ db.NAME.FIELD }} for a redis service; it has no
// DATABASE.
func redisField(db store.Service, field string) (string, bool) {
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
		return db.Env[redisPassword], true
	}
	return "", false
}

// redisArgs persists every write (AOF) and caps memory below the container
// limit, so redis refuses writes instead of being OOM-killed (the rest is
// headroom for the AOF rewrite fork and allocator overhead).
func redisArgs(svc store.Service) []string {
	args := []string{
		"redis-server",
		"--appendonly", "yes",
		"--dir", redisVolumeMount,
		"--requirepass", svc.Env[redisPassword],
	}
	if svc.MemoryMB > 0 {
		args = append(args, "--maxmemory", fmt.Sprintf("%dmb", svc.MemoryMB*3/4))
	}
	return args
}

func applyRedisSpec(cfg *container.Config, host *container.HostConfig, svc store.Service) {
	cfg.Cmd = redisArgs(svc)
	// redis-cli reads it: the healthcheck and the web terminal need no -a.
	cfg.Env = append(cfg.Env, "REDISCLI_AUTH="+svc.Env[redisPassword])
	cfg.Healthcheck = &container.HealthConfig{
		// PING answers LOADING while the AOF replays: not ready yet.
		Test:          []string{"CMD-SHELL", "redis-cli ping | grep -q PONG"},
		Interval:      10 * time.Second,
		Timeout:       5 * time.Second,
		StartPeriod:   30 * time.Second,
		StartInterval: time.Second,
		Retries:       3,
	}
	host.Mounts = append(host.Mounts, mount.Mount{
		Type:   mount.TypeVolume,
		Source: RedisVolume(svc.ID),
		Target: redisVolumeMount,
	})
	cfg.Labels[docker.LabelComponent] = string(store.ServiceKindRedis)
}
