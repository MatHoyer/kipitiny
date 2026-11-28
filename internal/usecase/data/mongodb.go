package data

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/kipit-labs/kipitiny/internal/usecase/identity"

	"github.com/kipit-labs/kipitiny/internal/domain"
)

const (
	DefaultMongoImage    = "mongo:8.2"
	DefaultMongoMemoryMB = 1024
	minMongoMemoryMB     = 512

	mongoUser     = "MONGO_INITDB_ROOT_USERNAME"
	MongoPassword = "MONGO_INITDB_ROOT_PASSWORD"
	// mongoDatabase is the default database of the connection string; an app
	// may use others (the user is the instance's root).
	mongoDatabase = "MONGO_INITDB_DATABASE"

	mongoVolumeMount = "/data/db"
	// mongoConfigMount is a VOLUME of the image, only used by config
	// servers: a tmpfs, so recreates don't leave anonymous volumes behind.
	MongoConfigMount = "/data/configdb"
	mongoPort        = 27017

	// WiredTiger checkpoints on shutdown.
	mongoStopTimeout = 60 * time.Second
)

var mongoManagedKeys = []string{mongoUser, MongoPassword, mongoDatabase}

func NewMongoEnv() map[string]string {
	return map[string]string{
		mongoUser:     "app",
		MongoPassword: identity.RandomPassword(),
		mongoDatabase: "app",
	}
}

// MongoVolume is the named volume holding a MongoDB service's data.
func MongoVolume(serviceID string) string {
	return "kipitiny-mongodata-" + strings.ToLower(serviceID)
}

func validateMongo(s domain.Service) error {
	if s.MemoryMB < minMongoMemoryMB {
		return fmt.Errorf("%w: mongodb needs at least %d MB of memory", ErrInvalid, minMongoMemoryMB)
	}
	for _, k := range mongoManagedKeys {
		if s.Env[k] == "" {
			return fmt.Errorf("%w: %s is required", ErrInvalid, k)
		}
	}
	return nil
}

// mongoURL authenticates against admin, where the image creates the user.
func mongoURL(db domain.Service) string {
	u := url.URL{
		Scheme:   "mongodb",
		User:     url.UserPassword(db.Env[mongoUser], db.Env[MongoPassword]),
		Host:     db.Name + ":" + strconv.Itoa(mongoPort),
		Path:     "/" + db.Env[mongoDatabase],
		RawQuery: "authSource=admin",
	}
	return u.String()
}

// mongoField resolves {{ db.NAME.FIELD }} for a MongoDB service.
func mongoField(db domain.Service, field string) (string, bool) {
	switch field {
	case "URL":
		return mongoURL(db), true
	case "HOST":
		return db.Name, true
	case "PORT":
		return strconv.Itoa(mongoPort), true
	case "USER":
		return db.Env[mongoUser], true
	case "PASSWORD":
		return db.Env[MongoPassword], true
	case "DATABASE":
		return db.Env[mongoDatabase], true
	}
	return "", false
}

// mongoArgs sizes the WiredTiger cache like mongod does from the machine's
// memory (half of what's above 1 GB, at least 256 MB), but from the
// container limit. The image's entrypoint prepends mongod to arguments
// starting with a dash.
func mongoArgs(memoryMB int) []string {
	cacheGB := max(0.25, float64(memoryMB-1024)/2/1024)
	return []string{"--wiredTigerCacheSizeGB", strconv.FormatFloat(cacheGB, 'f', 2, 64)}
}

// mongoFirstStartRestart lets Docker start a MongoDB again when its first
// start fails: the image's entrypoint stops its init server with
// `mongod --shutdown`, which returns once the lock file is released but
// before that server lets go of the port, so the real server can fail to
// bind it (exit 48, docker-library/mongo#730). Started again on the
// initialized data, the entrypoint skips the init server.
var mongoFirstStartRestart = domain.RestartPolicy{Name: domain.RestartOnFailure, MaxRetries: 1}

func applyMongoSpec(spec *domain.ContainerSpec, svc domain.Service) {
	if spec.Restart.Name == "" { // a service's container already has unless-stopped
		spec.Restart = mongoFirstStartRestart
	}
	spec.Cmd = mongoArgs(svc.MemoryMB)
	spec.Healthcheck = mongoHealthcheck()
	spec.Mounts = append(spec.Mounts, domain.Mount{
		Type:   domain.MountVolume,
		Source: MongoVolume(svc.ID),
		Target: mongoVolumeMount,
	})
	spec.Tmpfs = map[string]string{MongoConfigMount: ""}
	spec.Labels[domain.LabelComponent] = string(domain.ServiceKindMongoDB)
}

// mongoHealthcheck pings once the entrypoint has exec'd the server: the
// image creates the user with a temporary server first (forked, bound to
// 127.0.0.1), which must not count as ready. ping needs no authentication.
func mongoHealthcheck() *domain.HealthConfig {
	return &domain.HealthConfig{
		Test: []string{"CMD-SHELL",
			`[ "$(cat /proc/1/comm)" = mongod ] && mongosh --quiet --eval 'quit(db.adminCommand({ ping: 1 }).ok ? 0 : 1)'`},
		Interval:      10 * time.Second,
		Timeout:       10 * time.Second,
		StartPeriod:   90 * time.Second,
		StartInterval: 2 * time.Second,
		Retries:       3,
	}
}

// mongoAuthArgs authenticate the database tools as the service's user.
func mongoAuthArgs(svc domain.Service) []string {
	return mongoAuth(svc.Env[mongoUser], svc.Env[MongoPassword])
}

// mongoAuth passes the credentials as --flag=value: a password starting
// with "-" would otherwise be read as an option.
func mongoAuth(user, password string) []string {
	return []string{"--username=" + user, "--password=" + password, "--authenticationDatabase=admin"}
}
