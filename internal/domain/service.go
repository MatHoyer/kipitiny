package domain

import (
	"context"
	"time"

	"github.com/kipit-labs/kipitiny/internal/domain/anonymize"
)

// ServiceRepository stores services.
type ServiceRepository interface {
	CreateService(ctx context.Context, s Service) (Service, error)
	GetService(ctx context.Context, id string) (Service, error)
	ListServices(ctx context.Context, projectID string) ([]Service, error)
	UpdateService(ctx context.Context, s Service) (Service, error)
	RenameService(ctx context.Context, id, name string) error
	SetServiceTags(ctx context.Context, id string, tags []string) error
	// SetServiceAnalyticsShare sets the hash of the app's analytics link
	// (now as its creation), or removes it when empty.
	SetServiceAnalyticsShare(ctx context.Context, id, hash string) error
	// GetServiceByAnalyticsShare returns ErrNotFound for a hash no app has.
	GetServiceByAnalyticsShare(ctx context.Context, hash string) (Service, error)
	SetServiceAnonymize(ctx context.Context, id string, rules anonymize.Rules) error
	SetCurrentDeployment(ctx context.Context, serviceID, deploymentID string) error
	// SetDeploymentImage records the image a deployment runs, pinned to the
	// digest it was pulled at.
	SetDeploymentImage(ctx context.Context, id, image string, config Service) error
	// SetServiceStopped also clears Sleeping: the user's choice wins.
	SetServiceStopped(ctx context.Context, serviceID string, stopped bool) error
	// SetServiceImage records the image a service now deploys.
	SetServiceImage(ctx context.Context, serviceID, image string) error
	// ListAllServices returns every service of every project.
	ListAllServices(ctx context.Context) ([]Service, error)
	DeleteService(ctx context.Context, id string) error
	// SetServiceGitState records what a git sync applied to a service.
	SetServiceGitState(ctx context.Context, serviceID, specHash string, orphaned bool) error
	// SetServiceNetworks replaces the networks a service joins.
	SetServiceNetworks(ctx context.Context, serviceID string, networkIDs []string) error
}

type ServiceKind string

const (
	// StopTimeout is how long a container gets to stop, unless its service
	// sets its own grace.
	StopTimeout = 10 * time.Second
	// ProbeDir is where the probe volume is mounted in app containers: no
	// volume or file of a service may go there.
	ProbeDir = "/.kipitiny"
)

const (
	ServiceKindApp      ServiceKind = "app"
	ServiceKindPostgres ServiceKind = "postgres"
	ServiceKindRedis    ServiceKind = "redis"
	ServiceKindMySQL    ServiceKind = "mysql"
	ServiceKindMariaDB  ServiceKind = "mariadb"
	ServiceKindMongoDB  ServiceKind = "mongodb"
)

// IsDatabase reports whether the kind is a stateful single-replica service
// on a data volume, never public.
func (k ServiceKind) IsDatabase() bool {
	switch k {
	case ServiceKindPostgres, ServiceKindRedis, ServiceKindMySQL, ServiceKindMariaDB, ServiceKindMongoDB:
		return true
	}
	return false
}

// IsMySQL reports whether the kind speaks the MySQL protocol (MySQL or
// MariaDB).
func (k ServiceKind) IsMySQL() bool {
	return k == ServiceKindMySQL || k == ServiceKindMariaDB
}

type Service struct {
	ID        string `bun:"id,pk" json:"id"`
	ProjectID string `bun:"project_id" json:"projectId"`
	// ServerID mirrors the project's server.
	ServerID string      `bun:"server_id" json:"serverId"`
	Name     string      `bun:"name" json:"name"`
	Kind     ServiceKind `bun:"kind" json:"kind"`
	Image    string      `bun:"image" json:"image"`
	// Icon names the logo the UI shows (e.g. ghost), set by a template or
	// the user; empty picks one from the kind or image.
	Icon string `bun:"icon" json:"icon"`
	// Tags group services in the UI, like Icon set by a compose file or
	// the user.
	Tags []string `bun:"tags" json:"tags"`
	// Anonymize are a database's rules for anonymized preview seeds; nil
	// has none.
	Anonymize anonymize.Rules `bun:"anonymize" json:"anonymize"`
	// Replicas is always 1 for databases.
	Replicas int `bun:"replicas" json:"replicas"`
	// Port is the container port Traefik routes to; 0 when not public.
	Port int `bun:"port" json:"port"`
	// Domain is the public hostname; empty when not public.
	Domain string `bun:"domain" json:"domain"`
	// Analytics: the analytics server counts visits of Domain.
	Analytics bool `bun:"analytics" json:"analytics"`
	// AnalyticsSince is when Domain started being counted for this service:
	// the hostname's earlier visits were someone else's. Zero for a domain
	// set before it was recorded.
	AnalyticsSince time.Time `bun:"analytics_since,nullzero" json:"-"`
	// AnalyticsShare is the SHA-256 (hex) of the token of the app's public
	// analytics link, empty when not shared; AnalyticsSharedAt when it was
	// created. Set by SetServiceAnalyticsShare only.
	AnalyticsShare string `bun:"analytics_share" json:"-"`
	// AnalyticsScript and AnalyticsHit are the paths of Domain serving the
	// tracker script and taking hits (both or neither), so ad blockers
	// don't recognize them; empty uses /a.js and /e of the analytics domain.
	AnalyticsScript   string    `bun:"analytics_script" json:"analyticsScript"`
	AnalyticsHit      string    `bun:"analytics_hit" json:"analyticsHit"`
	AnalyticsSharedAt time.Time `bun:"analytics_shared_at,nullzero" json:"analyticsSharedAt,omitzero"`
	// Errors: the app's Sentry SDK reports to the analytics server, with
	// the DSN of ErrorsProject and ErrorsKey (kept while off, so turning it
	// back on keeps the DSN).
	Errors        bool              `bun:"errors" json:"errors"`
	ErrorsProject int64             `bun:"errors_project" json:"-"`
	ErrorsKey     string            `bun:"errors_key" json:"-"`
	Env           map[string]string `bun:"env" json:"env"`
	// Secrets names the Env entries that are write-only; the others are
	// plain variables.
	Secrets []string `bun:"secrets" json:"secrets"`
	// MemoryMB is the container memory limit; 0 means unlimited.
	MemoryMB int `bun:"memory_mb" json:"memoryMb"`
	// CPUs is the container CPU limit in cores (0.5 = half a core); 0 means
	// unlimited.
	CPUs float64 `bun:"cpus" json:"cpus"`
	// HealthPath is an HTTP path that must answer 2xx/3xx before a new
	// replica takes over; empty uses the image healthcheck or a stability wait.
	HealthPath string `bun:"health_path" json:"healthPath"`
	// PreDeploy runs once (sh -c) in a one-off container before a rollout,
	// e.g. migrations. A failure aborts the deploy.
	PreDeploy string `bun:"pre_deploy" json:"preDeploy"`
	// Healthcheck replaces the image's own and the injected probe (apps
	// only); the zero value has none.
	Healthcheck Healthcheck `bun:"healthcheck,type:text" json:"healthcheck"`
	// Volumes are named volumes an app mounts, shared by its replicas and
	// kept across deploys. Databases keep their data in their own volume.
	Volumes []Volume `bun:"volumes" json:"volumes"`
	// Middlewares apply to a public app's requests through Traefik.
	Middlewares Middlewares `bun:"middlewares,type:text" json:"middlewares"`
	// PublishedPorts are host ports bound straight to an app's container,
	// for traffic Traefik doesn't carry (game servers, MQTT…). An app with
	// published ports runs one replica.
	PublishedPorts []PublishedPort `bun:"published_ports" json:"publishedPorts"`
	// Routes send some paths of Domain to the app, each with its own scheme
	// and port; empty routes the whole domain to Port.
	Routes []Route `bun:"routes" json:"routes"`
	// StopGraceSeconds is how long an app gets to exit after SIGTERM before
	// it is killed (e.g. a game server saving its world); 0 means 10 s.
	StopGraceSeconds int `bun:"stop_grace_seconds" json:"stopGraceSeconds"`
	// HostNetwork runs an app in the host's network namespace (agents that
	// report on the host): no project network, no domain, one replica.
	HostNetwork bool `bun:"host_network" json:"hostNetwork"`
	// DockerSocket mounts the host's Docker socket in an app: "" none, "ro"
	// or "rw". Either gives the app control of the host.
	DockerSocket string `bun:"docker_socket" json:"dockerSocket"`
	// PreBackup runs (sh -c) in a running replica before each volume
	// backup, e.g. to flush to disk. A failure aborts the backup.
	PreBackup string `bun:"pre_backup" json:"preBackup"`
	// Runtime holds the rarer container settings (command, user, tmpfs…);
	// the zero value runs the image as built. Apps only.
	Runtime Runtime `bun:"runtime,type:text" json:"runtime"`
	// Files are written into an app's containers before they start: compose
	// configs, secrets and files bind-mounted from a git repository.
	Files []ServiceFile `bun:"files" json:"files"`
	// DependsOn names the services of the project that start before this
	// one (deploys, waking up).
	DependsOn []Dependency `bun:"depends_on" json:"dependsOn"`
	// ProjectNetworks are the project's own networks the service is on, by
	// compose name ("default" is the project network); empty is the
	// default one only. Services share only the networks they're both on.
	ProjectNetworks []string `bun:"project_networks" json:"projectNetworks"`
	// Networks are the IDs of the networks created by hand that the service
	// joins besides its project networks. Set on their own, or by a compose
	// file whose service lists networks (external ones).
	Networks            []string `bun:"networks" json:"networks"`
	CurrentDeploymentID string   `bun:"current_deployment_id" json:"currentDeploymentId"`
	// Preview is "only" for a service of the project's previews alone, never
	// deployed in the project itself (e.g. a mail catcher), "skip" for one
	// previews leave out; "" runs in both.
	Preview string `bun:"preview" json:"preview"`
	// Stopped is the desired run state after the user stopped the service.
	Stopped bool `bun:"stopped" json:"stopped"`
	// Sleeping is set while the manager keeps the service stopped for its
	// project being idle. Waking the project, a stop or a deploy clears it.
	Sleeping bool `bun:"sleeping" json:"sleeping"`
	// GitSpecHash is the hash of the compose block a git sync last applied.
	GitSpecHash string `bun:"git_spec_hash" json:"-"`
	// Orphaned marks a database of a git project that the compose file no
	// longer lists: kept until deleted by hand.
	Orphaned bool `bun:"orphaned" json:"orphaned"`

	CreatedAt time.Time `bun:"created_at" json:"createdAt"`
	UpdatedAt time.Time `bun:"updated_at" json:"updatedAt"`
}

// Volume is a named volume mounted at Path in every replica of an app.
type Volume struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	ReadOnly bool   `json:"readOnly,omitempty"`
	// External names an existing Docker volume mounted as is: kipitiny
	// never creates, labels or removes it. Admins only.
	External string `json:"external,omitempty"`
	// DriverOpts are the local driver's options a new volume is created
	// with (an NFS share…). Admins only: they can mount host paths.
	DriverOpts map[string]string `json:"driverOpts,omitempty"`
}

// HostAccess reports whether v reaches beyond the service's own data.
func (v Volume) HostAccess() bool { return v.External != "" || len(v.DriverOpts) > 0 }

// PublishedPort binds HostPort on the server to ContainerPort of an app.
type PublishedPort struct {
	// HostIP is the server address the port is bound on; empty is every one.
	HostIP        string `json:"hostIp,omitempty"`
	HostPort      int    `json:"hostPort"`
	ContainerPort int    `json:"containerPort"`
	// Protocol is "tcp" or "udp".
	Protocol string `json:"protocol"`
}

// Route sends the paths of an app's domain that start with one of Paths to
// the app, ahead of a catch-all on the same domain.
type Route struct {
	Paths []string `json:"paths"`
	// Scheme is how Traefik talks to the container: "" (HTTP/1.1) or "h2c"
	// (HTTP/2 cleartext, for gRPC).
	Scheme string `json:"scheme,omitempty"`
	// Port is the container port; 0 is the app's.
	Port int `json:"port,omitempty"`
}

// Dependency is a service another one waits for.
type Dependency struct {
	Service string `json:"service"`
	// Condition is "" (started), "healthy" (ready) or "completed" (a job
	// that exited 0).
	Condition string `json:"condition,omitempty"`
}

// ServiceFile is a file written into an app's containers before they start.
type ServiceFile struct {
	// Path is where it goes in the container (absolute).
	Path string `json:"path"`
	// Content is left out of API responses (see Size).
	Content []byte `json:"content,omitempty"`
	Size    int    `json:"size"`
	// Mode is its permission bits; 0 is 0644.
	Mode int64 `json:"mode,omitempty"`
	// Secret marks a compose secret: its references ({{ project.X }}…)
	// resolve at deploy, and an export writes it as a variable.
	Secret bool `json:"secret,omitempty"`
	// Source is what the compose file named: config:NAME, secret:NAME, or
	// the bind-mounted path in the repository.
	Source string `json:"source,omitempty"`
}
