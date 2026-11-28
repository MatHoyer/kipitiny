// Package compose maps a project to a docker-compose file and back: the
// subset of the compose spec kipitiny can run, plus a per-service
// x-kipitiny block for what compose has no field for (domain, middlewares,
// database kind…). Docker Compose ignores x-* keys, so an exported file
// still runs locally. It knows nothing about Docker or the store's rules:
// core validates what Parse returns.
package compose

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/domain/anonymize"
)

// File is a project as a compose file.
type File struct {
	// Name is the compose project name.
	Name     string
	Services map[string]Service
	// X is the top-level x-kipitiny block.
	X FileExt
}

// FileExt holds the project's shared variables, referenced by services as
// {{ project.NAME }}.
type FileExt struct {
	Variables map[string]string `yaml:"variables,omitempty"`
	// Secrets names the write-only Variables.
	Secrets []string `yaml:"secrets,omitempty"`
}

// Service is one compose service, in kipitiny's terms.
type Service struct {
	Image       string
	Environment map[string]string
	Ports       []domain.PublishedPort
	Volumes     []domain.Volume
	// Replicas is 0 when the file doesn't set it.
	Replicas         int
	MemoryMB         int
	CPUs             float64
	StopGraceSeconds int
	// HostNetwork is network_mode: host.
	HostNetwork bool
	// DockerSocket is "ro" or "rw" when the service mounts the host's
	// Docker socket.
	DockerSocket string
	// Healthcheck is compose's healthcheck key; nil when the file has none.
	// Tagged so the hash of a service that doesn't use it stays the same.
	Healthcheck *domain.Healthcheck `json:",omitempty"`
	// Runtime holds command, entrypoint, user, tmpfs…; nil when the file
	// sets none. Tagged like Healthcheck.
	Runtime *domain.Runtime `json:",omitempty"`
	// Files are the configs, secrets and repository files bind-mounted.
	Files []domain.ServiceFile `json:",omitempty"`
	// Networks are the file's own networks the service is on ("default"
	// included); nil without a networks key. ExternalNetworks are the
	// external ones, by name: networks created by hand in kipitiny.
	Networks         []string `json:",omitempty"`
	ExternalNetworks []string `json:",omitempty"`
	// DependsOn are the services of the file this one starts after.
	DependsOn []domain.Dependency `json:",omitempty"`
	// VarEnv lists the environment keys whose whole value is a ${NAME}
	// variable: secrets, unless x-kipitiny.secrets says otherwise.
	VarEnv []string `json:"-"`
	X      Ext
	// disabled is a service whose profiles kipitiny doesn't run.
	disabled bool
}

// DockerSocket is the only path a service may bind-mount: the host's
// Docker socket, at the same path in the container.
const DockerSocket = "/var/run/docker.sock"

// Ext is a service's x-kipitiny block.
type Ext struct {
	// Kind is app, postgres, redis, mysql, mariadb or mongodb; empty means
	// app, unless the image is the official postgres or redis one.
	Kind       string `yaml:"kind,omitempty"`
	Domain     string `yaml:"domain,omitempty"`
	Port       int    `yaml:"port,omitempty"`
	HealthPath string `yaml:"health_path,omitempty"`
	PreDeploy  string `yaml:"pre_deploy,omitempty"`
	PreBackup  string `yaml:"pre_backup,omitempty"`
	Icon       string `yaml:"icon,omitempty"`
	// Analytics counts the domain's visits. Tagged so the hash of a service
	// without it stays the same.
	Analytics bool `yaml:"analytics,omitempty" json:",omitempty"`
	// Errors takes the errors of the app's Sentry SDK. Tagged like Analytics.
	Errors bool `yaml:"errors,omitempty" json:",omitempty"`
	// Preview is "only" (a service of previews alone, e.g. a mail catcher)
	// or "skip" (left out of previews). Tagged so the hash of a service
	// without it stays the same.
	Preview string `yaml:"preview,omitempty" json:",omitempty"`
	// Tags group services in the UI. Tagged so the hash of a service
	// without any stays the same.
	Tags    []string `yaml:"tags,omitempty" json:",omitempty"`
	Secrets []string `yaml:"secrets,omitempty"`
	// Password is a database's password, usually ${NAME}; empty generates
	// one.
	Password    string       `yaml:"password,omitempty"`
	Middlewares *Middlewares `yaml:"middlewares,omitempty"`
	// Routes send some paths of Domain to the service (gRPC on h2c, an API
	// next to another service's frontend). Tagged so the hash of a service
	// without any stays the same.
	Routes []Route `yaml:"routes,omitempty" json:",omitempty"`
	// Uptime is an app's uptime check; nil has none. Tagged so the hash of a
	// service without one stays the same.
	Uptime *Uptime `yaml:"uptime,omitempty" json:",omitempty"`
	// Jobs are an app's scheduled jobs, by name. Tagged so the hash of a
	// service without any stays the same.
	Jobs map[string]Job `yaml:"jobs,omitempty" json:",omitempty"`
	// Anonymize are a database's rules for anonymized preview seeds; nil
	// has none, an empty set holds nothing personal. Tagged so the hash of
	// a service without any stays the same.
	Anonymize *anonymize.Rules `yaml:"anonymize,omitempty" json:",omitempty"`
}

// Route is a set of path prefixes of the domain routed to the service.
type Route struct {
	Paths []string `yaml:"paths"`
	// Scheme is http (default) or h2c.
	Scheme string `yaml:"scheme,omitempty"`
	Port   int    `yaml:"port,omitempty"`
}

// Job is a scheduled job: a command run with sh -c on a cron schedule.
type Job struct {
	Command  string  `yaml:"command"`
	Schedule string  `yaml:"schedule"`
	Timeout  Seconds `yaml:"timeout,omitempty"`
	Paused   bool    `yaml:"paused,omitempty"`
}

// Uptime is an uptime check: zero values take kipitiny's defaults.
type Uptime struct {
	Path           string  `yaml:"path,omitempty"`
	Interval       Seconds `yaml:"interval,omitempty"`
	Timeout        Seconds `yaml:"timeout,omitempty"`
	ExpectedStatus int     `yaml:"expected_status,omitempty"`
	Paused         bool    `yaml:"paused,omitempty"`
}

// Seconds is a duration in whole seconds, written as a compose duration
// (1m30s) and read from one or from a number of seconds.
type Seconds int

func (s Seconds) MarshalYAML() (any, error) {
	return (time.Duration(s) * time.Second).String(), nil
}

func (s *Seconds) UnmarshalYAML(n *yaml.Node) error {
	secs, err := parseSeconds(n.Value)
	if n.Kind != yaml.ScalarNode || err != nil {
		return fmt.Errorf("line %d: %q is not a duration", n.Line, n.Value)
	}
	*s = Seconds(secs)
	return nil
}

func parseSeconds(v string) (int, error) {
	if secs, err := strconv.Atoi(v); err == nil {
		return secs, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, err
	}
	return int((d + time.Second - 1) / time.Second), nil
}

type Middlewares struct {
	BasicAuth   []BasicAuthUser   `yaml:"basic_auth,omitempty"`
	IPAllowList []string          `yaml:"ip_allowlist,omitempty"`
	RateLimit   *RateLimit        `yaml:"rate_limit,omitempty"`
	Headers     map[string]string `yaml:"headers,omitempty"`
}

// BasicAuthUser has a bcrypt Hash or a password manager Ref, never a
// plain password.
type BasicAuthUser struct {
	Name string `yaml:"name"`
	Hash string `yaml:"hash,omitempty"`
	Ref  string `yaml:"ref,omitempty"`
}

type RateLimit struct {
	Average int `yaml:"average"`
	Burst   int `yaml:"burst,omitempty"`
}

// ExtKey is the service key kipitiny reads its own settings from.
const ExtKey = "x-kipitiny"

// supportedServiceKeys are the compose service keys Parse reads.
var supportedServiceKeys = []string{"image", "build", "environment", "ports", "expose", "volumes", "deploy", "mem_limit", "cpus", "stop_grace_period", "network_mode", "healthcheck",
	"command", "entrypoint", "user", "working_dir", "labels", "tmpfs", "shm_size", "ulimits", "sysctls", "read_only", "init", "stop_signal",
	"tty", "stdin_open", "extra_hosts", "cap_add", "cap_drop", "devices", "privileged", "security_opt", "configs", "secrets", "env_file", "extends", "depends_on", "restart", "networks", "profiles", "platform", ExtKey}

// Keys that change nothing kipitiny can't do itself (it orders, networks
// and restarts services on its own): dropped with a warning. Any other
// unknown key is an error, as ignoring it would run something else than
// the file says.
var (
	ignoredServiceKeys = []string{
		"container_name", "hostname",
		"links", "logging", "pull_policy",
	}
	ignoredTopKeys = []string{"version"}
)

// ServiceNames returns f's service names, sorted.
func (f File) ServiceNames() []string {
	return slices.Sorted(maps.Keys(f.Services))
}
