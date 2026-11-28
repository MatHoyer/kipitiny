// Package servers is the machines apps run on: this one and remote ones over
// SSH, their Docker clients, the reconciler that keeps Docker in line with the
// store, host checks and updates, resource stats and alerts, the cleanup,
// terminals, logs, and the manager's own status and updates.
package servers

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kipit-labs/kipitiny/internal/config"
	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/notify"
	"github.com/kipit-labs/kipitiny/internal/progress"
	"github.com/kipit-labs/kipitiny/internal/usecase/identity"
	"github.com/kipit-labs/kipitiny/internal/usecase/network"
)

// The errors use cases share, under the names handlers know them by.
var (
	ErrInvalid = domain.ErrInvalid
	ErrBusy    = domain.ErrBusy
)

type Servers struct {
	// cfg is the manager's configuration, shared with core.
	cfg   *config.Config
	store domain.Store
	// cron reads and runs the cleanup's schedule and the audit pruning.
	cron domain.Cron
	mgr  Manager
	log  *slog.Logger
	// bg ends when the manager stops: loops and the cleanup run under it.
	bg context.Context
	// pool holds a Docker client per server, remote ones connected on
	// demand over SSH.
	pool Pool
	// registry is where the update check reads the manager's published versions.
	registry Registry
	// steps is where the cleanup reports its step, and where the update
	// preflight reads the restore tests', shared with core.
	steps *progress.Steps
	// probes: server ID -> *domain.Mount of the health probe, shared with
	// deploy (a deleted server forgets its own).
	probes *sync.Map

	reconcileKick chan struct{}
	update        updateState
	sched         scheduler
	cleaning      atomic.Bool // a cleanup is running
	securityBusy  atomic.Bool // a security check started by hand is running
	hostActionMu  sync.Mutex  // starting an update install or a reboot is check-then-start
	stats         statsState
}

// Pool hands out the Docker client of each server: the local daemon, and
// remote ones connected on demand over SSH (from RemoteDocker).
type Pool interface {
	// For returns the client of a server ("" or "local": this one). One
	// that can't be built fails every call.
	For(serverID string) domain.ContainerRuntime
	// Invalidate drops a remote server's client after it changed.
	Invalidate(serverID string)
}

// Registry reads a public image's tags and labels from its registry.
type Registry interface {
	Tags(ctx context.Context, ref string) ([]string, error)
	Labels(ctx context.Context, ref, tag string) (map[string]string, error)
}

// New builds the servers use cases around the pool of Docker clients; bg is
// the manager's background context.
func New(bg context.Context, cfg *config.Config, store domain.Store, pool Pool, registry Registry, cron domain.Cron, steps *progress.Steps, probes *sync.Map, mgr Manager, log *slog.Logger) *Servers {
	return &Servers{cfg: cfg, store: store, pool: pool, registry: registry, cron: cron, mgr: mgr, log: log, bg: bg, steps: steps, probes: probes, reconcileKick: make(chan struct{}, 1)}
}

// DockerFor returns the Docker client of a server ("" or "local": this one).
func (c *Servers) DockerFor(serverID string) domain.ContainerRuntime {
	return c.pool.For(serverID)
}

// Manager is what servers uses from the rest of the manager.
type Manager interface {
	// GoBackground runs an operation the manager waits for on shutdown,
	// GoLoop a loop that ends with it.
	GoBackground(f func()) error
	GoLoop(f func()) error
	// RunningOperations counts the operations in flight, BusyServices
	// lists the services one holds: an update would cut them short.
	RunningOperations() int
	BusyServices() []string

	// Identity: the audit log and its pruning, permissions, the password
	// manager helpers a crash left behind.
	Audit(ctx context.Context, action, target string, status int, actionErr error)
	PruneAudit(ctx context.Context)
	RequireOn(ctx context.Context, serviceID string, p identity.Perm) error
	CleanupSecretHelpers(ctx context.Context, dk domain.ContainerRuntime) error

	// Network: what the manager runs on each server (Traefik, the tunnel)
	// and a server's public IP and tunnel.
	EnsureTraefik(ctx context.Context, sv domain.Server) error
	EnsureTunnel(ctx context.Context, sv domain.Server) error
	RefreshProxy(ctx context.Context)
	DetectedPublicIP(serverID string) string
	SetServerNetwork(ctx context.Context, id string, in network.ServerNetwork) (domain.Server, error)

	// Monitoring: notifications, the analytics container, the SMTP setting
	// of older versions.
	Notify(e notify.Event, path, key string)
	EnsureAnalytics(ctx context.Context) error
	MigrateSMTPSetting(ctx context.Context) error

	// Deploy: what the reconciler keeps in line on each server, a service's
	// containers, deployment logs, registry credentials.
	ReconcileService(ctx context.Context, project domain.Project, svc domain.Service) error
	TrackHealth(project domain.Project, svc domain.Service, cts []domain.Container, now time.Time)
	ForgetHealth(serverID string, known map[string]bool)
	EnsureNetworks(ctx context.Context, sv domain.Server) error
	ActiveContainers(ctx context.Context, svc domain.Service) ([]domain.Container, error)
	WakeFor(ctx context.Context, svc domain.Service) error
	DeployLogPath(serviceID, deployID string) string
	RegistryAuth(ctx context.Context, image string) string

	// Data: the restore tests a crash left behind.
	CleanupRestoreTests(ctx context.Context, dk domain.ContainerRuntime) error

	// Projects: the containers of jobs that no longer run.
	RemoveStaleJobContainers(ctx context.Context, dk domain.ContainerRuntime, all []domain.Container)
}
