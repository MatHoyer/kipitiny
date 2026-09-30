package core

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/distribution/reference"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"

	"github.com/MatHoyer/kipitiny/internal/docker"
	"github.com/MatHoyer/kipitiny/internal/store"
)

const (
	DefaultPostgresImage    = "postgres:17-alpine"
	defaultPostgresMemoryMB = 512
	minPostgresMemoryMB     = 128

	pgUser     = "POSTGRES_USER"
	pgPassword = "POSTGRES_PASSWORD"
	pgDatabase = "POSTGRES_DB"

	// PGDATA sits in a subdirectory of the volume so the layout is the same
	// for every image version (18+ changed the default data path).
	pgVolumeMount = "/var/lib/postgresql/data"
	pgDataDir     = pgVolumeMount + "/pgdata"

	postgresStopTimeout  = 60 * time.Second
	postgresReadyTimeout = 2 * time.Minute
)

// pgManagedKeys are generated once; changing them after initdb would not
// change the actual credentials, so they are read-only.
var pgManagedKeys = []string{pgUser, pgPassword, pgDatabase}

func newPostgresEnv() map[string]string {
	return map[string]string{
		pgUser:     "app",
		pgPassword: randomToken(24),
		pgDatabase: "app",
	}
}

// PostgresVolume is the named volume holding a postgres service's data.
func PostgresVolume(serviceID string) string {
	return "kipitiny-pgdata-" + strings.ToLower(serviceID)
}

// postgresMajor extracts the major version from the image tag ("17-alpine",
// "16.4" → 17, 16). Returns 0 when the tag doesn't start with a number.
func postgresMajor(image string) int {
	ref, err := reference.ParseNormalizedNamed(image)
	if err != nil {
		return 0
	}
	tagged, ok := ref.(reference.Tagged)
	if !ok {
		return 0
	}
	tag := tagged.Tag()
	end := strings.IndexFunc(tag, func(r rune) bool { return r < '0' || r > '9' })
	if end == -1 {
		end = len(tag)
	}
	n, _ := strconv.Atoi(tag[:end])
	return n
}

func validatePostgres(s store.Service) error {
	if s.Replicas != 1 {
		return fmt.Errorf("%w: postgres services always run a single replica", ErrInvalid)
	}
	if s.Domain != "" || s.Port != 0 {
		return fmt.Errorf("%w: databases are never public", ErrInvalid)
	}
	if s.MemoryMB < minPostgresMemoryMB {
		return fmt.Errorf("%w: postgres needs at least %d MB of memory", ErrInvalid, minPostgresMemoryMB)
	}
	if postgresMajor(s.Image) == 0 {
		return fmt.Errorf("%w: pin a major version in the image tag (e.g. postgres:17-alpine)", ErrInvalid)
	}
	for _, k := range pgManagedKeys {
		if s.Env[k] == "" {
			return fmt.Errorf("%w: %s is required", ErrInvalid, k)
		}
	}
	return nil
}

// checkPostgresUpdate refuses changes that would break an existing cluster.
func checkPostgresUpdate(old, updated store.Service) error {
	if postgresMajor(old.Image) != postgresMajor(updated.Image) {
		return fmt.Errorf("%w: changing the postgres major version needs a dump and restore; create a new database service instead", ErrInvalid)
	}
	for _, k := range pgManagedKeys {
		if old.Env[k] != updated.Env[k] {
			return fmt.Errorf("%w: %s cannot be changed after creation", ErrInvalid, k)
		}
	}
	return nil
}

// DatabaseURL is the connection string apps of the project use.
func DatabaseURL(db store.Service) string {
	u := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(db.Env[pgUser], db.Env[pgPassword]),
		Host:   db.Name + ":5432",
		Path:   "/" + db.Env[pgDatabase],
	}
	return u.String()
}

type Connection struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Database string `json:"database"`
	User     string `json:"user"`
	Password string `json:"password"`
	URL      string `json:"url"`
}

// PostgresConnection reveals a database's credentials (explicit action; they
// are masked everywhere else).
func (c *Core) PostgresConnection(ctx context.Context, id string) (Connection, error) {
	svc, err := c.store.GetService(ctx, id)
	if err != nil {
		return Connection{}, err
	}
	if svc.Kind != store.ServiceKindPostgres {
		return Connection{}, fmt.Errorf("%w: not a postgres service", ErrInvalid)
	}
	return Connection{
		Host:     svc.Name,
		Port:     5432,
		Database: svc.Env[pgDatabase],
		User:     svc.Env[pgUser],
		Password: svc.Env[pgPassword],
		URL:      DatabaseURL(svc),
	}, nil
}

// postgresTuning sizes memory settings from the container limit.
func postgresTuning(memoryMB int) []string {
	return []string{
		"postgres",
		"-c", fmt.Sprintf("shared_buffers=%dMB", memoryMB/4),
		"-c", fmt.Sprintf("effective_cache_size=%dMB", memoryMB/2),
		"-c", fmt.Sprintf("maintenance_work_mem=%dMB", max(memoryMB/16, 16)),
		"-c", "max_connections=100",
	}
}

func applyPostgresSpec(cfg *container.Config, host *container.HostConfig, svc store.Service) {
	cfg.Cmd = postgresTuning(svc.MemoryMB)
	cfg.Env = append(cfg.Env, "PGDATA="+pgDataDir)
	cfg.Healthcheck = postgresHealthcheck(svc.Env[pgUser], svc.Env[pgDatabase])
	// Room for pg_dump/pg_restore over exec without hitting /dev/shm limits.
	host.ShmSize = 128 << 20
	host.Mounts = append(host.Mounts, mount.Mount{
		Type:   mount.TypeVolume,
		Source: PostgresVolume(svc.ID),
		Target: pgVolumeMount,
	})
	cfg.Labels[docker.LabelComponent] = "postgres"
}

// postgresHealthcheck probes over TCP: during first-start initdb the image
// runs a temporary server on the Unix socket only, which must not count as
// ready (it restarts right after).
func postgresHealthcheck(user, db string) *container.HealthConfig {
	return &container.HealthConfig{
		Test:          []string{"CMD-SHELL", fmt.Sprintf("pg_isready -h 127.0.0.1 -U %s -d %s", user, db)},
		Interval:      10 * time.Second,
		Timeout:       5 * time.Second,
		StartPeriod:   60 * time.Second,
		StartInterval: time.Second,
		Retries:       3,
	}
}

func stopTimeoutFor(svc store.Service) time.Duration {
	if svc.Kind == store.ServiceKindPostgres {
		return postgresStopTimeout
	}
	return stopTimeout
}
