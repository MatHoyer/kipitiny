// Package deploy is how apps run: services and their containers, compose
// files, deploys, rollouts and rollbacks, previews, environments, registries,
// networks, renames and sleep.
package deploy

import (
	"context"
	"log/slog"
	"sync"

	"github.com/kipit-labs/kipitiny/internal/config"
	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/notify"
	"github.com/kipit-labs/kipitiny/internal/usecase/compose"
	"github.com/kipit-labs/kipitiny/internal/usecase/network"
)

// The errors use cases share, under the names handlers know them by.
var (
	ErrInvalid = domain.ErrInvalid
	ErrBusy    = domain.ErrBusy
)

type Deployer struct {
	// cfg is the manager's configuration, shared with core.
	cfg   *config.Config
	store domain.Store
	// cron reads the schedules of the jobs a compose file declares.
	cron domain.Cron
	mgr  Manager
	log  *slog.Logger
	// bg ends when the manager stops: deploys and loops run under it.
	bg context.Context
	// probes: server ID -> *domain.Mount of the health probe (nil: none
	// there), shared with core (servers forget theirs on delete).
	probes *sync.Map

	sleep  sleepState
	health healthState
}

// New builds the deploy use cases; bg is the manager's background context.
func New(bg context.Context, cfg *config.Config, store domain.Store, cron domain.Cron, probes *sync.Map, mgr Manager, log *slog.Logger) *Deployer {
	return &Deployer{cfg: cfg, store: store, cron: cron, mgr: mgr, log: log, bg: bg, probes: probes}
}

// Manager is what deploy uses from the rest of the manager.
type Manager interface {
	// DockerFor is the Docker client of a server.
	DockerFor(serverID string) domain.ContainerRuntime
	// GoBackground runs an operation the manager waits for on shutdown,
	// GoLoop a loop that ends with it.
	GoBackground(f func()) error
	GoLoop(f func()) error
	// LockService holds a service for one operation, ErrBusy when another
	// one runs; LockServiceBackground is the reconciler's, which gives up
	// on a held service.
	LockService(id string) (unlock func(), err error)
	LockServiceBackground(id string) (unlock func(), err error)
	// Kick asks the reconciler for a pass soon.
	Kick()
	// RemoveProject deletes a project with its previews and containers,
	// unconfirmed (an expired or closed preview).
	RemoveProject(ctx context.Context, p domain.Project) error

	// Projects: CheckGitOwned refuses a change to a project a git
	// repository owns; WithGitFile calls fn with its compose file at g's
	// branch and a reader of the repository's other files.
	CheckGitOwned(ctx context.Context, projectID string) error
	WithGitFile(ctx context.Context, g domain.ProjectGit, fn func(data []byte, commit string, files func(rel string) ([]domain.ServiceFile, error)) error) error
	// Jobs: a compose file declares them, a deleted service takes them.
	DeleteJob(ctx context.Context, j domain.Job) error
	ReloadJobs(ctx context.Context) error
	ForgetServiceJobs(serviceID string)
	RemoveJobContainers(ctx context.Context, dk domain.ContainerRuntime, serviceID string) error
	JobRunningIn(projectID string) bool

	// Identity: the audit log, the password check, password manager references.
	Audit(ctx context.Context, action, target string, status int, actionErr error)
	ConfirmedUser(ctx context.Context, password string) (domain.User, error)
	CheckSecretScheme(ctx context.Context, ref string) error
	ResolveSecrets(ctx context.Context, refs []string) (map[string]string, error)

	// Network: a service's routing and domains, DNS, ports, basic auth.
	RouteFor(ctx context.Context, svc domain.Service) network.Routing
	CheckDomain(ctx context.Context, s domain.Service, serverID string, others []domain.Service) error
	DNSStatusOf(ctx context.Context, host string) *network.DNSStatus
	KickDNS()
	ViaTunnel(ctx context.Context, serverID string) bool
	TunnelToken(sv domain.Server) string
	// ManagerUpstream is where a server's Traefik reaches the manager.
	ManagerUpstream(ctx context.Context, dk domain.ContainerRuntime) (string, error)
	CheckPortsFree(ctx context.Context, s domain.Service) error
	DockerSocketFor(ctx context.Context, svc domain.Service) string
	HashBasicAuth(ctx context.Context, svc *domain.Service) (int, error)
	CheckBasicAuthSchemes(ctx context.Context, m domain.Middlewares) error

	// Monitoring: notifications, uptime checks, error tracking.
	Notify(e notify.Event, path, key string)
	NotifyOOM(ctx context.Context, serviceID, replica string)
	SaveUptime(ctx context.Context, chk domain.UptimeCheck) error
	RemoveUptime(ctx context.Context, serviceID string) error
	ErrorsEnv(ctx context.Context, project domain.Project, svc domain.Service) map[string]string
	ErrorsIdentity(ctx context.Context, svc *domain.Service) error

	// Data: a preview's anonymized copies and seeded backups, the files
	// helper of a service.
	SeedAnonymized(ctx context.Context, parent domain.Project, f compose.File, parentSvcs []domain.Service) (map[string]string, error)
	RefreshAnonymized(ctx context.Context, projectID string, force bool) error
	RefreshAllAnonymized(ctx context.Context)
	LogAnonymizeErr(err error)
	LatestSeedable(ctx context.Context, serviceID string) (domain.Backup, bool, error)
	RestoreBackup(ctx context.Context, backupID, targetServiceID, confirm string) (domain.Restore, error)
	CloseFilesHelper(serviceID string)
}
