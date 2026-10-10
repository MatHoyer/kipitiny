package core

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"time"

	"github.com/moby/moby/api/types/container"

	"github.com/MatHoyer/kipitiny/internal/store"
)

// databaseReadyTimeout bounds the wait for a recreated database to pass its
// healthcheck (initdb, crash recovery or an AOF load can take a while).
const databaseReadyTimeout = 2 * time.Minute

// DataVolume is the named volume holding a database's data; empty for apps.
func DataVolume(svc store.Service) string {
	switch svc.Kind {
	case store.ServiceKindPostgres:
		return PostgresVolume(svc.ID)
	case store.ServiceKindRedis:
		return RedisVolume(svc.ID)
	case store.ServiceKindMySQL, store.ServiceKindMariaDB:
		return MySQLVolume(svc.ID)
	case store.ServiceKindMongoDB:
		return MongoVolume(svc.ID)
	}
	return ""
}

// validateDatabase checks what every database kind shares, then the kind's
// own rules.
func validateDatabase(s store.Service) error {
	if s.HealthPath != "" || s.Healthcheck.Set() || s.PreDeploy != "" {
		return fmt.Errorf("%w: databases have a built-in healthcheck and no pre-deploy command", ErrInvalid)
	}
	if s.Replicas != 1 {
		return fmt.Errorf("%w: databases always run a single replica", ErrInvalid)
	}
	if s.Domain != "" || s.Port != 0 {
		return fmt.Errorf("%w: databases are never public", ErrInvalid)
	}
	for k, v := range s.Env {
		for _, m := range envRefRe.FindAllStringSubmatch(v, -1) {
			if m[2] != "" {
				return fmt.Errorf("%w: %s: a database can't reference another database (%s)", ErrInvalid, k, refName(m))
			}
			if m[4] != "" {
				// Backups and connection strings read the credentials as stored.
				return fmt.Errorf("%w: %s: a database can't use password manager references (%s)", ErrInvalid, k, refName(m))
			}
		}
	}
	switch s.Kind {
	case store.ServiceKindPostgres:
		return validatePostgres(s)
	case store.ServiceKindRedis:
		return validateRedis(s)
	case store.ServiceKindMySQL, store.ServiceKindMariaDB:
		return validateMySQL(s)
	case store.ServiceKindMongoDB:
		return validateMongo(s)
	}
	return nil
}

// checkDatabaseUpdate refuses changes that would break existing data.
func checkDatabaseUpdate(old, updated store.Service) error {
	// The credentials are generated at creation and baked into the data
	// directory (postgres initdb) or shared with apps: edits would silently
	// do nothing or lock apps out.
	if !maps.Equal(old.Env, updated.Env) || !slices.Equal(old.Secrets, updated.Secrets) {
		return fmt.Errorf("%w: a database's environment is fixed at creation", ErrInvalid)
	}
	if old.Kind == store.ServiceKindPostgres {
		return checkPostgresUpdate(old, updated)
	}
	return nil
}

// DatabaseURL is the connection string apps of the project use.
func DatabaseURL(db store.Service) string {
	v, _ := databaseField(db, "URL")
	return v
}

// databaseField resolves {{ db.NAME.FIELD }}; false when the kind has no such
// field.
func databaseField(db store.Service, field string) (string, bool) {
	switch db.Kind {
	case store.ServiceKindPostgres:
		return postgresField(db, field)
	case store.ServiceKindRedis:
		return redisField(db, field)
	case store.ServiceKindMySQL, store.ServiceKindMariaDB:
		return mysqlField(db, field)
	case store.ServiceKindMongoDB:
		return mongoField(db, field)
	}
	return "", false
}

type Connection struct {
	Host string `json:"host"`
	Port int    `json:"port"`
	// Database is empty for kinds without named databases (redis); for
	// MongoDB it's the connection string's default one.
	Database string `json:"database,omitempty"`
	User     string `json:"user"`
	Password string `json:"password"`
	URL      string `json:"url"`
}

// DatabaseConnection reveals a database's credentials (explicit action; they
// are masked everywhere else).
func (c *Core) DatabaseConnection(ctx context.Context, id string) (Connection, error) {
	svc, err := c.store.GetService(ctx, id)
	if err != nil {
		return Connection{}, err
	}
	if !svc.Kind.IsDatabase() {
		return Connection{}, fmt.Errorf("%w: not a database", ErrInvalid)
	}
	field := func(f string) string {
		v, _ := databaseField(svc, f)
		return v
	}
	port, _ := strconv.Atoi(field("PORT"))
	return Connection{
		Host:     svc.Name,
		Port:     port,
		Database: field("DATABASE"),
		User:     field("USER"),
		Password: field("PASSWORD"),
		URL:      field("URL"),
	}, nil
}

func applyDatabaseSpec(cfg *container.Config, host *container.HostConfig, svc store.Service) {
	switch svc.Kind {
	case store.ServiceKindPostgres:
		applyPostgresSpec(cfg, host, svc)
	case store.ServiceKindRedis:
		applyRedisSpec(cfg, host, svc)
	case store.ServiceKindMySQL, store.ServiceKindMariaDB:
		applyMySQLSpec(cfg, host, svc)
	case store.ServiceKindMongoDB:
		applyMongoSpec(cfg, host, svc)
	}
}

func stopTimeoutFor(svc store.Service) time.Duration {
	switch svc.Kind {
	case store.ServiceKindPostgres:
		return postgresStopTimeout
	case store.ServiceKindRedis:
		return redisStopTimeout
	case store.ServiceKindMySQL, store.ServiceKindMariaDB:
		return mysqlStopTimeout
	case store.ServiceKindMongoDB:
		return mongoStopTimeout
	}
	if svc.StopGraceSeconds > 0 {
		return time.Duration(svc.StopGraceSeconds) * time.Second
	}
	return stopTimeout
}
