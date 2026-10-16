package compose

import (
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/MatHoyer/kipitiny/internal/store"
)

// databaseKeys are the environment keys a database's image reads its user
// and database name from, by kind: kept (at creation) as they name what
// the apps connect to. Other keys are generated.
var databaseKeys = map[string][]string{
	"postgres": {"POSTGRES_USER", "POSTGRES_DB"},
	"mysql":    {"MYSQL_USER", "MYSQL_DATABASE"},
	"mariadb":  {"MYSQL_USER", "MYSQL_DATABASE"},
	"mongodb":  {"MONGO_INITDB_ROOT_USERNAME", "MONGO_INITDB_DATABASE"},
}

// databasePasswordKeys give the password, as x-kipitiny.password does.
var databasePasswordKeys = []string{"POSTGRES_PASSWORD", "MYSQL_PASSWORD", "MARIADB_PASSWORD", "MONGO_INITDB_ROOT_PASSWORD", "REDIS_PASSWORD"}

// InitDir is where database images run scripts from on their first start.
const InitDir = "/docker-entrypoint-initdb.d"

// databaseEnv keeps the database's user, name and password from its
// environment and drops the rest.
func (p *parser) databaseEnv(s *Service) {
	keep := map[string]string{}
	var ignored []string
	for k, v := range s.Environment {
		if alias, ok := strings.CutPrefix(k, "MARIADB_"); ok && s.X.Kind == "mariadb" && alias != "PASSWORD" {
			k = "MYSQL_" + alias // MariaDB's image reads both
		}
		switch {
		case slices.Contains(databaseKeys[s.X.Kind], k):
			keep[k] = v
		case slices.Contains(databasePasswordKeys, k) && s.X.Password == "":
			s.X.Password = v
		case slices.Contains(databasePasswordKeys, k):
		default:
			ignored = append(ignored, k)
		}
	}
	if len(ignored) > 0 {
		slices.Sort(ignored)
		p.warn("environment %s of a %s service is ignored (generated)", strings.Join(ignored, ", "), s.X.Kind)
	}
	s.Environment = nil
	if len(keep) > 0 {
		s.Environment = keep
	}
	s.VarEnv = slices.DeleteFunc(s.VarEnv, func(k string) bool { _, ok := keep[k]; return !ok })
}

// initFiles keeps a database's files in InitDir (scripts run on its first
// start) and drops the others.
func (p *parser) initFiles(kind string, files []store.ServiceFile) []store.ServiceFile {
	var out []store.ServiceFile
	for _, f := range files {
		if kind != "redis" && strings.HasPrefix(f.Path, InitDir+"/") {
			out = append(out, f)
		} else {
			p.warn("file %s of a %s service is ignored: only %s scripts are (the manager runs it)", f.Path, kind, InitDir)
		}
	}
	return out
}

// restartPolicy reads deploy.restart_policy into rt.Restart, like restart.
func (p *parser) restartPolicy(n *yaml.Node, rt *store.Runtime) {
	cond, attempts := "any", 0
	for k, v := range pairs(n) {
		switch k {
		case "condition":
			cond = p.str(v)
		case "max_attempts":
			attempts = p.int(v)
		default:
			p.warn("deploy.restart_policy.%s is ignored", k)
		}
	}
	switch cond {
	case "any":
	case "none":
		rt.Restart = "no"
	case "on-failure":
		rt.Restart = "on-failure"
		if attempts > 0 {
			rt.Restart += ":" + strconv.Itoa(attempts)
		}
	default:
		p.fail("deploy.restart_policy.condition %s is not supported (none, on-failure or any)", cond)
	}
}
