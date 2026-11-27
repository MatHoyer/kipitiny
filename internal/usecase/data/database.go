package data

import (
	"context"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"

	"github.com/kipit-labs/kipitiny/internal/compose"
	"github.com/kipit-labs/kipitiny/internal/domain"
)

// databaseReadyTimeout bounds the wait for a recreated database to pass its
// healthcheck (initdb, crash recovery or an AOF load can take a while).
const DatabaseReadyTimeout = 2 * time.Minute

// DataVolume is the named volume holding a database's data; empty for apps.
func DataVolume(svc domain.Service) string {
	switch svc.Kind {
	case domain.ServiceKindPostgres:
		return PostgresVolume(svc.ID)
	case domain.ServiceKindRedis:
		return RedisVolume(svc.ID)
	case domain.ServiceKindMySQL, domain.ServiceKindMariaDB:
		return MySQLVolume(svc.ID)
	case domain.ServiceKindMongoDB:
		return MongoVolume(svc.ID)
	}
	return ""
}

// ValidateDatabase checks what every database kind shares, then the kind's
// own rules.
// databaseNameKeys are a database's user and database name in its env: a
// new one takes them from its input (a compose file), the rest is generated.
var DatabaseNameKeys = []string{PGUser, PGDatabase, mysqlUser, mysqlDatabase, mongoUser, mongoDatabase}

var databaseNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,62}$`)

func ValidateDatabase(s domain.Service) error {
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
		for _, m := range domain.EnvRefRe.FindAllStringSubmatch(v, -1) {
			if m[2] != "" {
				return fmt.Errorf("%w: %s: a database can't reference another database (%s)", ErrInvalid, k, domain.RefName(m))
			}
			if m[4] != "" {
				// Backups and connection strings read the credentials as stored.
				return fmt.Errorf("%w: %s: a database can't use password manager references (%s)", ErrInvalid, k, domain.RefName(m))
			}
		}
	}
	for _, k := range DatabaseNameKeys {
		if v, ok := s.Env[k]; ok && !databaseNameRe.MatchString(v) {
			return fmt.Errorf("%w: %s: letters, digits and underscores, at most 63, not starting with a digit", ErrInvalid, k)
		}
	}
	for _, f := range s.Files {
		if s.Kind == domain.ServiceKindRedis || !strings.HasPrefix(f.Path, compose.InitDir+"/") {
			return fmt.Errorf("%w: a database's files go in %s (scripts run on its first start)", ErrInvalid, compose.InitDir)
		}
	}
	switch s.Kind {
	case domain.ServiceKindPostgres:
		return validatePostgres(s)
	case domain.ServiceKindRedis:
		return validateRedis(s)
	case domain.ServiceKindMySQL, domain.ServiceKindMariaDB:
		return validateMySQL(s)
	case domain.ServiceKindMongoDB:
		return validateMongo(s)
	}
	return nil
}

// CheckDatabaseUpdate refuses changes that would break existing data.
func CheckDatabaseUpdate(old, updated domain.Service) error {
	// The credentials are generated at creation and baked into the data
	// directory (postgres initdb) or shared with apps: edits would silently
	// do nothing or lock apps out.
	if !maps.Equal(old.Env, updated.Env) || !slices.Equal(old.Secrets, updated.Secrets) {
		return fmt.Errorf("%w: a database's environment is fixed at creation", ErrInvalid)
	}
	if old.Kind == domain.ServiceKindPostgres {
		return checkPostgresUpdate(old, updated)
	}
	return nil
}

// DatabaseURL is the connection string apps of the project use.
func DatabaseURL(db domain.Service) string {
	v, _ := DatabaseField(db, "URL")
	return v
}

// DatabaseField resolves {{ db.NAME.FIELD }}; false when the kind has no such
// field.
func DatabaseField(db domain.Service, field string) (string, bool) {
	switch db.Kind {
	case domain.ServiceKindPostgres:
		return postgresField(db, field)
	case domain.ServiceKindRedis:
		return redisField(db, field)
	case domain.ServiceKindMySQL, domain.ServiceKindMariaDB:
		return mysqlField(db, field)
	case domain.ServiceKindMongoDB:
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
func (c *Data) DatabaseConnection(ctx context.Context, id string) (Connection, error) {
	svc, err := c.store.GetService(ctx, id)
	if err != nil {
		return Connection{}, err
	}
	if !svc.Kind.IsDatabase() {
		return Connection{}, fmt.Errorf("%w: not a database", ErrInvalid)
	}
	field := func(f string) string {
		v, _ := DatabaseField(svc, f)
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

func ApplyDatabaseSpec(cfg *container.Config, host *container.HostConfig, svc domain.Service) {
	switch svc.Kind {
	case domain.ServiceKindPostgres:
		applyPostgresSpec(cfg, host, svc)
	case domain.ServiceKindRedis:
		applyRedisSpec(cfg, host, svc)
	case domain.ServiceKindMySQL, domain.ServiceKindMariaDB:
		applyMySQLSpec(cfg, host, svc)
	case domain.ServiceKindMongoDB:
		applyMongoSpec(cfg, host, svc)
	}
}

func StopTimeoutFor(svc domain.Service) time.Duration {
	switch svc.Kind {
	case domain.ServiceKindPostgres:
		return postgresStopTimeout
	case domain.ServiceKindRedis:
		return redisStopTimeout
	case domain.ServiceKindMySQL, domain.ServiceKindMariaDB:
		return mysqlStopTimeout
	case domain.ServiceKindMongoDB:
		return mongoStopTimeout
	}
	if svc.StopGraceSeconds > 0 {
		return time.Duration(svc.StopGraceSeconds) * time.Second
	}
	return domain.StopTimeout
}
