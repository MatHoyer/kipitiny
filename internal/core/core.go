// Package core is the service layer. REST handlers and MCP tools are thin
// adapters over it; validation and permissions are enforced here.
package core

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kipit-labs/kipitiny/internal/usecase/data"
	"github.com/kipit-labs/kipitiny/internal/usecase/deploy"
	"github.com/kipit-labs/kipitiny/internal/usecase/identity"
	"github.com/kipit-labs/kipitiny/internal/usecase/monitoring"
	"github.com/kipit-labs/kipitiny/internal/usecase/network"

	"github.com/kipit-labs/kipitiny/internal/usecase/pending"

	"github.com/kipit-labs/kipitiny/internal/config"
	"github.com/kipit-labs/kipitiny/internal/docker"
	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/progress"
)

// The errors use cases share, under the names handlers know them by.
var (
	ErrInvalid      = domain.ErrInvalid
	ErrBusy         = domain.ErrBusy
	ErrShuttingDown = domain.ErrShuttingDown
)

type Core struct {
	// Users, sign-in, tokens, permissions and password managers.
	*identity.Identity
	// Domains, DNS, Traefik, routes, middlewares, ports and tunnels.
	*network.Network
	// Notifications, email, push, uptime checks, analytics and errors.
	*monitoring.Monitoring
	// Databases and the data browser, backups, restores, volumes and files.
	*data.Data
	// Services, compose files, deploys, previews, networks and sleep.
	*deploy.Deployer

	cfg   config.Config
	store domain.Store
	pool  *docker.Pool
	log   *slog.Logger

	// Background work runs under bg. On Shutdown, operations (deploys,
	// backups) get to finish first; loops (reconciler) just stop with bg.
	bg     context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup // all background work
	ops    sync.WaitGroup // operations only
	// running counts the operations in flight, for the update's preflight.
	running atomic.Int64
	bgMu    sync.Mutex
	closed  bool
	locks   sync.Map // service ID -> *serviceLock
	busy    sync.Map // service ID -> struct{}: its lock is held

	sched scheduler
	// steps: what the running operations are at (backups and restores in
	// data, the cleanup here).
	steps         progress.Steps
	reconcileKick chan struct{}
	probes        sync.Map // server ID -> *mount.Mount (nil: no probe there)
	update        updateState
	gitKick       chan string // project IDs to sync
	gitFlows      pending.Map[gitFlow]
	gitTokens     gitTokens
	cleaning      atomic.Bool // a cleanup is running
	securityBusy  atomic.Bool // a security check started by hand is running
	hostActionMu  sync.Mutex  // starting an update install or a reboot is check-then-start
	stats         statsState
	jobs          jobsState
}

// New builds the core around the local Docker client; remote servers are
// connected on demand over SSH.
func New(cfg config.Config, s domain.Store, local *docker.Client, log *slog.Logger) *Core {
	bg, cancel := context.WithCancel(context.Background())
	c := &Core{cfg: cfg, store: s, log: log, bg: bg, cancel: cancel, reconcileKick: make(chan struct{}, 1), gitKick: make(chan string, 16)}
	c.pool = docker.NewPool(local, c.connectServer)
	c.Monitoring = monitoring.New(bg, &c.cfg, s, monitoringPorts{c}, log)
	c.Identity = identity.New(cfg, s, identity.SecretKinds(cfg, local), c.Monitoring, log)
	c.Network = network.New(bg, &c.cfg, s, networkPorts{c}, log)
	c.Data = data.New(bg, &c.cfg, s, &c.steps, dataPorts{c}, log)
	c.Deployer = deploy.New(bg, &c.cfg, s, &c.probes, deployPorts{c}, log)
	return c
}

// Bootstrap prepares host-level resources the manager relies on.
func (c *Core) Bootstrap(ctx context.Context) error {
	if n, err := c.store.FailRunningDeployments(ctx, "interrupted by manager restart"); err != nil {
		return err
	} else if n > 0 {
		c.log.Warn("marked interrupted deployments as failed", "count", n)
	}
	if err := c.store.FailRunningOperations(ctx, "interrupted by manager restart"); err != nil {
		return err
	}
	if err := c.MigrateSMTPSetting(ctx); err != nil {
		return err
	}
	if c.cfg.Domain != "" && !c.cfg.Traefik.Enabled {
		c.log.Warn("KIPITINY_DOMAIN is ignored without the managed Traefik; route it in your own proxy", "domain", c.cfg.Domain)
	}
	if c.cfg.Tunnel.Token != "" && !c.cfg.Traefik.Enabled {
		c.log.Warn("KIPITINY_CLOUDFLARE_TUNNEL_TOKEN is ignored without the managed Traefik")
	}
	servers, err := c.store.ListServers(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, sv := range servers {
		// Nothing runs yet: what restore tests and volume helpers left
		// behind is from a crash. (Later bootstraps must not touch them.)
		if err := c.CleanupRestoreTests(ctx, c.dockerFor(sv.ID)); err != nil {
			errs = append(errs, fmt.Errorf("server %s: %w", sv.Name, err))
			continue
		}
		if err := c.bootstrapServer(ctx, sv); err != nil {
			errs = append(errs, fmt.Errorf("server %s: %w", sv.Name, err))
		}
	}
	return errors.Join(errs...)
}

// bootstrapServer prepares a Docker host: proxy network, Traefik, and
// cleanup of terminals left by a crash.
func (c *Core) bootstrapServer(ctx context.Context, sv domain.Server) error {
	dk := c.dockerFor(sv.ID)
	progress.Report(ctx, ServerStepNetwork)
	if err := dk.EnsureNetwork(ctx, docker.ProxyNetwork); err != nil {
		return err
	}
	if err := c.cleanupTerminals(ctx, dk); err != nil {
		return err
	}
	if sv.Kind == domain.ServerLocal {
		c.cleanupUpdater(ctx, dk)
		if err := c.CleanupSecretHelpers(ctx, dk); err != nil {
			c.log.Warn("removing password manager helpers failed", "err", err)
		}
	}
	if c.cfg.Traefik.Enabled {
		progress.Report(ctx, ServerStepTraefik)
		if err := c.EnsureTraefik(ctx, sv); err != nil {
			return fmt.Errorf("traefik: %w", err)
		}
		progress.Report(ctx, ServerStepTunnel)
		if err := c.EnsureTunnel(ctx, sv); err != nil {
			return fmt.Errorf("cloudflared: %w", err)
		}
	}
	return nil
}

// DrainTimeout bounds how long Shutdown waits for running operations. The
// container's stop timeout must exceed it plus shutdownCancelGrace.
const DrainTimeout = 4*time.Minute + 30*time.Second

// shutdownCancelGrace is how long cancelled work gets to clean up.
const shutdownCancelGrace = 15 * time.Second

// Shutdown refuses new operations, waits until ctx for running ones (a deploy
// cut short by an upgrade would have to be redone), then cancels whatever is
// left and waits briefly for it to wind down.
func (c *Core) Shutdown(ctx context.Context) error {
	c.stopScheduler()
	c.bgMu.Lock()
	c.closed = true
	c.bgMu.Unlock()

	var err error
	select {
	case <-waitDone(&c.ops):
	default:
		c.log.Info("waiting for running operations to finish")
		select {
		case <-waitDone(&c.ops):
		case <-ctx.Done():
			c.log.Warn("cancelling unfinished operations")
			err = ctx.Err()
		}
	}
	c.cancel()
	c.CloseFilesHelpers()
	if cerr := c.CloseSecretProviders(context.WithoutCancel(ctx)); cerr != nil {
		c.log.Warn("removing password manager helpers failed", "err", cerr)
	}
	select {
	case <-waitDone(&c.wg):
	case <-time.After(shutdownCancelGrace):
		err = errors.New("background work did not stop after cancellation")
	}
	return err
}

func waitDone(wg *sync.WaitGroup) <-chan struct{} {
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	return done
}

// BehindTunnel reports whether the manager's traffic arrives through a
// Cloudflare tunnel.
func (c *Core) BehindTunnel(ctx context.Context) bool {
	return c.cfg.Traefik.Enabled && c.ViaTunnel(ctx, domain.LocalServerID)
}

// dockerFor returns the Docker client of a server ("" or "local": this one).
func (c *Core) dockerFor(serverID string) *docker.Client {
	return c.pool.For(serverID)
}

// goBackground runs f as an operation that Shutdown lets finish, unless
// shutdown started.
func (c *Core) goBackground(f func()) error {
	return c.track(f, true)
}

// goLoop runs f until bg is cancelled; Shutdown doesn't wait for it first.
func (c *Core) goLoop(f func()) error {
	return c.track(f, false)
}

func (c *Core) track(f func(), op bool) error {
	c.bgMu.Lock()
	defer c.bgMu.Unlock()
	if c.closed {
		return ErrShuttingDown
	}
	c.wg.Add(1)
	if op {
		c.ops.Add(1)
		c.running.Add(1)
	}
	go func() {
		defer c.wg.Done()
		if op {
			defer c.ops.Done()
			defer c.running.Add(-1)
		}
		f()
	}()
	return nil
}

// serviceLock is one service's lock.
type serviceLock struct {
	mu       sync.Mutex
	released sync.Cond // on mu
	held     bool
	// background: the reconciler holds it, for one short pass users don't see.
	background bool
}

// lockService serializes mutating operations on one service. It never
// waits for another operation (a deploy can take minutes): a second caller
// gets ErrBusy. It only waits out a reconciler pass, which would otherwise
// fail what the user just asked, like a rename right after scaling.
func (c *Core) lockService(id string) (unlock func(), err error) {
	return c.acquireService(id, false)
}

// lockServiceBackground is the reconciler's: it never waits, and gives up on
// a service an operation holds.
func (c *Core) lockServiceBackground(id string) (unlock func(), err error) {
	return c.acquireService(id, true)
}

func (c *Core) acquireService(id string, background bool) (func(), error) {
	m, ok := c.locks.Load(id)
	if !ok {
		l := &serviceLock{}
		l.released.L = &l.mu
		m, _ = c.locks.LoadOrStore(id, l)
	}
	l := m.(*serviceLock)
	l.mu.Lock()
	defer l.mu.Unlock()
	for l.held && l.background && !background {
		l.released.Wait()
	}
	if l.held {
		return nil, ErrBusy
	}
	l.held, l.background = true, background
	c.busy.Store(id, struct{}{})
	return func() {
		l.mu.Lock()
		l.held, l.background = false, false
		c.busy.Delete(id)
		l.mu.Unlock()
		l.released.Broadcast()
	}, nil
}

type Status struct {
	Version     string       `json:"version"`
	Update      UpdateInfo   `json:"update"`
	Docker      *docker.Info `json:"docker"`
	DockerError string       `json:"dockerError,omitempty"`
	// Database is the size of the manager's SQLite files, absent when they
	// can't be read.
	Database *DatabaseSize `json:"database,omitempty"`
}

type DatabaseSize struct {
	SizeBytes int64 `json:"sizeBytes"`
	// WALBytes is the write-ahead log not yet checkpointed into the file.
	WALBytes int64 `json:"walBytes"`
}

func (c *Core) databaseSize() *DatabaseSize {
	fi, err := os.Stat(c.cfg.DBPath())
	if err != nil {
		return nil
	}
	d := &DatabaseSize{SizeBytes: fi.Size()}
	if wal, err := os.Stat(c.cfg.DBPath() + "-wal"); err == nil {
		d.WALBytes = wal.Size()
	}
	return d
}

func (c *Core) Status(ctx context.Context) Status {
	if a, ok := identity.ActorFrom(ctx); ok && !a.Admin() {
		return Status{Version: c.cfg.Version} // the rest is about the server
	}
	st := Status{Version: c.cfg.Version, Update: c.Update(), Database: c.databaseSize()}
	info, err := c.dockerFor(domain.LocalServerID).Info(ctx)
	if err != nil {
		st.DockerError = err.Error()
		return st
	}
	st.Docker = &info
	return st
}

func (c *Core) ListProjects(ctx context.Context) ([]domain.Project, error) {
	ps, err := c.store.ListProjects(ctx)
	ps = slices.DeleteFunc(ps, func(p domain.Project) bool { return !identity.Visible(ctx, p.ID) })
	for i := range ps {
		ps[i] = deploy.MaskedProject(ps[i])
	}
	return ps, err
}

// CreateProject creates a project on a server (this one when serverID is empty).
func (c *Core) CreateProject(ctx context.Context, name, serverID string) (domain.Project, error) {
	if err := identity.CheckFeature(ctx, identity.FeatureCreateProjects); err != nil {
		return domain.Project{}, err
	}
	if !domain.ValidName(name) {
		return domain.Project{}, fmt.Errorf("%w: name must be lowercase letters, digits and dashes (max 40)", ErrInvalid)
	}
	if serverID == "" {
		serverID = domain.LocalServerID
	}
	if _, err := c.store.GetServer(ctx, serverID); errors.Is(err, domain.ErrNotFound) {
		return domain.Project{}, fmt.Errorf("%w: unknown server", ErrInvalid)
	} else if err != nil {
		return domain.Project{}, err
	}
	p, err := c.store.CreateProject(ctx, domain.Project{Name: name, ServerID: serverID})
	if err != nil {
		return domain.Project{}, err
	}
	if err := c.GrantCreator(ctx, p.ID); err != nil {
		return p, err
	}
	if err := c.dockerFor(p.ServerID).EnsureNetwork(ctx, docker.ProjectNetwork(p.ID)); err != nil {
		return p, fmt.Errorf("project created but network setup failed: %w", err)
	}
	return p, nil
}

func (c *Core) GetProject(ctx context.Context, id string) (domain.Project, error) {
	p, err := c.store.GetProject(ctx, id)
	return deploy.MaskedProject(p), err
}

// DeleteProject removes every container and volume of the project's services,
// its record and its private network. The caller must confirm with the name.
func (c *Core) DeleteProject(ctx context.Context, id, confirm string) error {
	p, err := c.store.GetProject(ctx, id)
	if err != nil {
		return err
	}
	if confirm != p.Name {
		return fmt.Errorf("%w: confirm with the project name", ErrInvalid)
	}
	return c.deleteProject(ctx, p)
}

// deleteProject is DeleteProject without the confirmation; a project's
// previews go first.
func (c *Core) deleteProject(ctx context.Context, p domain.Project) error {
	id := p.ID
	all, err := c.store.ListProjects(ctx)
	if err != nil {
		return err
	}
	for _, child := range all {
		if child.ParentID == id {
			if err := c.deleteProject(ctx, child); err != nil {
				return fmt.Errorf("preview %s: %w", child.Preview.Name, err)
			}
		}
	}
	svcs, err := c.store.ListServices(ctx, id)
	if err != nil {
		return err
	}
	// Hold every service so nothing (deploy, backup, reconciler) recreates
	// containers while the project goes away.
	for _, s := range svcs {
		unlock, err := c.lockService(s.ID)
		if err != nil {
			return fmt.Errorf("%s: %w", s.Name, err)
		}
		defer unlock()
	}
	for _, s := range svcs {
		if err := c.RemoveServiceContainers(ctx, s); err != nil {
			return err
		}
	}
	if err := c.store.DeleteProject(ctx, id); err != nil {
		return err
	}
	for _, s := range svcs {
		c.RemoveDeployLogs(s.ID)
		c.forgetServiceJobs(s.ID)
	}
	dk := c.dockerFor(p.ServerID)
	nets, err := dk.ListNetworks(ctx)
	if err != nil {
		return err
	}
	for _, n := range nets { // the networks of its compose file
		if strings.HasPrefix(n.Name, docker.ProjectNetwork(id)+"-") {
			if err := dk.RemoveNetwork(ctx, n.Name); err != nil {
				return err
			}
		}
	}
	return dk.RemoveNetwork(ctx, docker.ProjectNetwork(id))
}
