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

// MySQL and MariaDB share this file: same environment, protocol and dump
// format, only the image and the client binaries' names differ.
const (
	DefaultMySQLImage    = "mysql:8.4"
	DefaultMariaDBImage  = "mariadb:11.8"
	defaultMySQLMemoryMB = 512
	minMySQLMemoryMB     = 256

	mysqlUser         = "MYSQL_USER"
	mysqlPassword     = "MYSQL_PASSWORD"
	mysqlDatabase     = "MYSQL_DATABASE"
	mysqlRootPassword = "MYSQL_ROOT_PASSWORD"

	mysqlVolumeMount = "/var/lib/mysql"
	mysqlPort        = 3306

	// InnoDB flushes its buffer pool on shutdown.
	mysqlStopTimeout = 60 * time.Second
)

// mysqlManagedKeys are generated once and only read by the image's first
// start (MariaDB's image reads the MYSQL_ names too).
var mysqlManagedKeys = []string{mysqlUser, mysqlPassword, mysqlDatabase, mysqlRootPassword}

// newMySQLEnv makes a new service's credentials: an app user owning one
// database, and root for backups and restores.
func newMySQLEnv() map[string]string {
	return map[string]string{
		mysqlUser:         "app",
		mysqlPassword:     randomToken(24),
		mysqlDatabase:     "app",
		mysqlRootPassword: randomToken(24),
	}
}

// MySQLVolume is the named volume holding a MySQL or MariaDB service's data.
func MySQLVolume(serviceID string) string {
	return "kipitiny-mysqldata-" + strings.ToLower(serviceID)
}

func defaultMySQLImage(k store.ServiceKind) string {
	if k == store.ServiceKindMariaDB {
		return DefaultMariaDBImage
	}
	return DefaultMySQLImage
}

// mysqlBin names a client tool in the kind's image: MariaDB's images only
// ship the mariadb-* names.
func mysqlBin(k store.ServiceKind, tool string) string {
	if k != store.ServiceKindMariaDB {
		return tool
	}
	switch tool {
	case "mysql":
		return "mariadb"
	case "mysqldump":
		return "mariadb-dump"
	case "mysqladmin":
		return "mariadb-admin"
	}
	return tool
}

func mysqlLabel(k store.ServiceKind) string {
	if k == store.ServiceKindMariaDB {
		return "MariaDB"
	}
	return "MySQL"
}

func validateMySQL(s store.Service) error {
	if s.MemoryMB < minMySQLMemoryMB {
		return fmt.Errorf("%w: %s needs at least %d MB of memory", ErrInvalid, mysqlLabel(s.Kind), minMySQLMemoryMB)
	}
	for _, k := range mysqlManagedKeys {
		if s.Env[k] == "" {
			return fmt.Errorf("%w: %s is required", ErrInvalid, k)
		}
	}
	return nil
}

func mysqlURL(db store.Service) string {
	u := url.URL{
		Scheme: "mysql",
		User:   url.UserPassword(db.Env[mysqlUser], db.Env[mysqlPassword]),
		Host:   db.Name + ":" + strconv.Itoa(mysqlPort),
		Path:   "/" + db.Env[mysqlDatabase],
	}
	return u.String()
}

// mysqlField resolves {{ db.NAME.FIELD }} for a MySQL or MariaDB service.
func mysqlField(db store.Service, field string) (string, bool) {
	switch field {
	case "URL":
		return mysqlURL(db), true
	case "HOST":
		return db.Name, true
	case "PORT":
		return strconv.Itoa(mysqlPort), true
	case "USER":
		return db.Env[mysqlUser], true
	case "PASSWORD":
		return db.Env[mysqlPassword], true
	case "DATABASE":
		return db.Env[mysqlDatabase], true
	}
	return "", false
}

// mysqlArgs sizes InnoDB from the container limit; the performance schema
// alone takes a few hundred MB on MySQL, so it's off. The image's entrypoint
// prepends the server binary to arguments starting with a dash.
func mysqlArgs(memoryMB int) []string {
	return []string{
		fmt.Sprintf("--innodb-buffer-pool-size=%dM", max(memoryMB/2, 64)),
		"--max-connections=100",
		"--performance-schema=OFF",
	}
}

func applyMySQLSpec(cfg *container.Config, host *container.HostConfig, svc store.Service) {
	cfg.Cmd = mysqlArgs(svc.MemoryMB)
	cfg.Healthcheck = mysqlHealthcheck(svc.Kind)
	host.Mounts = append(host.Mounts, mount.Mount{
		Type:   mount.TypeVolume,
		Source: MySQLVolume(svc.ID),
		Target: mysqlVolumeMount,
	})
	cfg.Labels[docker.LabelComponent] = string(svc.Kind)
}

// mysqlHealthcheck pings over TCP: the image initializes the data directory
// with a temporary server started with --skip-networking, which must not
// count as ready. The ping succeeds as soon as the server answers.
func mysqlHealthcheck(k store.ServiceKind) *container.HealthConfig {
	return &container.HealthConfig{
		Test:          []string{"CMD-SHELL", `MYSQL_PWD="$MYSQL_ROOT_PASSWORD" ` + mysqlBin(k, "mysqladmin") + " -h 127.0.0.1 -uroot --silent ping"},
		Interval:      10 * time.Second,
		Timeout:       5 * time.Second,
		StartPeriod:   90 * time.Second,
		StartInterval: time.Second,
		Retries:       3,
	}
}

// mysqlExecEnv authenticates the client tools as root without the password
// on the command line.
func mysqlExecEnv(svc store.Service) []string {
	return []string{"MYSQL_PWD=" + svc.Env[mysqlRootPassword]}
}

// mysqlIdent quotes an identifier.
func mysqlIdent(s string) string {
	return "`" + strings.ReplaceAll(s, "`", "``") + "`"
}
