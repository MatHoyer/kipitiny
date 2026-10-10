package core

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"

	"github.com/MatHoyer/kipitiny/internal/docker"
	"github.com/MatHoyer/kipitiny/internal/store"
)

const (
	DefaultMongoImage    = "mongo:8.2"
	defaultMongoMemoryMB = 1024
	minMongoMemoryMB     = 512

	mongoUser     = "MONGO_INITDB_ROOT_USERNAME"
	mongoPassword = "MONGO_INITDB_ROOT_PASSWORD"
	// mongoDatabase is the default database of the connection string; an app
	// may use others (the user is the instance's root).
	mongoDatabase = "MONGO_INITDB_DATABASE"

	mongoVolumeMount = "/data/db"
	// mongoConfigMount is a VOLUME of the image, only used by config
	// servers: a tmpfs, so recreates don't leave anonymous volumes behind.
	mongoConfigMount = "/data/configdb"
	mongoPort        = 27017

	// WiredTiger checkpoints on shutdown.
	mongoStopTimeout = 60 * time.Second
)

var mongoManagedKeys = []string{mongoUser, mongoPassword, mongoDatabase}

func newMongoEnv() map[string]string {
	return map[string]string{
		mongoUser:     "app",
		mongoPassword: randomToken(24),
		mongoDatabase: "app",
	}
}

// MongoVolume is the named volume holding a MongoDB service's data.
func MongoVolume(serviceID string) string {
	return "kipitiny-mongodata-" + strings.ToLower(serviceID)
}

func validateMongo(s store.Service) error {
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
func mongoURL(db store.Service) string {
	u := url.URL{
		Scheme:   "mongodb",
		User:     url.UserPassword(db.Env[mongoUser], db.Env[mongoPassword]),
		Host:     db.Name + ":" + strconv.Itoa(mongoPort),
		Path:     "/" + db.Env[mongoDatabase],
		RawQuery: "authSource=admin",
	}
	return u.String()
}

// mongoField resolves {{ db.NAME.FIELD }} for a MongoDB service.
func mongoField(db store.Service, field string) (string, bool) {
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
		return db.Env[mongoPassword], true
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

func applyMongoSpec(cfg *container.Config, host *container.HostConfig, svc store.Service) {
	cfg.Cmd = mongoArgs(svc.MemoryMB)
	cfg.Healthcheck = mongoHealthcheck()
	host.Mounts = append(host.Mounts, mount.Mount{
		Type:   mount.TypeVolume,
		Source: MongoVolume(svc.ID),
		Target: mongoVolumeMount,
	})
	host.Tmpfs = map[string]string{mongoConfigMount: ""}
	cfg.Labels[docker.LabelComponent] = string(store.ServiceKindMongoDB)
}

// mongoHealthcheck pings once the entrypoint has exec'd the server: the
// image creates the user with a temporary server first (forked, bound to
// 127.0.0.1), which must not count as ready. ping needs no authentication.
func mongoHealthcheck() *container.HealthConfig {
	return &container.HealthConfig{
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
func mongoAuthArgs(svc store.Service) []string {
	return []string{"--username", svc.Env[mongoUser], "--password", svc.Env[mongoPassword], "--authenticationDatabase", "admin"}
}
